package wa

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waAdv"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func jid(user, server string) types.JID { return types.NewJID(user, server) }

// The contact name is the address-book full name, else the first name, else the verified business name. The push name
// is a different column and never used; blanks count as empty; the result is in JID order.
func TestContactNamePreference(t *testing.T) {
	pn := func(u string) types.JID { return jid(u, types.DefaultUserServer) }
	lid := func(u string) types.JID { return jid(u, types.HiddenUserServer) }
	in := map[types.JID]types.ContactInfo{
		pn("3"):  {Found: true, FullName: "Layla Khan", FirstName: "Layla", BusinessName: "Khan Trading", PushName: "lay"},
		pn("2"):  {Found: true, FirstName: "Omar", BusinessName: "Omar Co", PushName: "om"},
		pn("1"):  {Found: true, BusinessName: "Shop", PushName: "shopper"},
		pn("4"):  {Found: true, PushName: "only a push name"},
		pn("5"):  {Found: true, FullName: "   ", FirstName: "\t", BusinessName: " "},
		pn("6"):  {Found: true, FullName: "  Sara  "},
		lid("9"): {Found: true, FullName: "Under a LID", RedactedPhone: "+1∙∙∙∙∙∙∙∙80"},
		pn("7"):  {},
	}
	want := []ContactName{
		{"1@s.whatsapp.net", "Shop"},
		{"2@s.whatsapp.net", "Omar"},
		{"3@s.whatsapp.net", "Layla Khan"},
		{"6@s.whatsapp.net", "Sara"},
		{"9@lid", "Under a LID"},
	}
	if got := ContactNames(in); !reflect.DeepEqual(got, want) {
		t.Errorf("ContactNames = %v\nwant %v", got, want)
	}
	if got := ContactNames(nil); len(got) != 0 {
		t.Errorf("nothing in, %v out", got)
	}
}

type fakeSource struct {
	mu     sync.Mutex
	calls  int
	out    map[types.JID]types.ContactInfo
	err    error
	gate   chan struct{} // when set, a call blocks until it is closed or ctx ends
	onCall func()        // when set, runs as a call starts
	in     chan struct{} // when set, signalled as a call starts
}

func (f *fakeSource) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	f.mu.Lock()
	f.calls++
	out, err, gate, in := f.out, f.err, f.gate, f.in
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall()
	}
	if in != nil {
		in <- struct{}{}
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return out, err
}
func (f *fakeSource) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeSink struct {
	mu    sync.Mutex
	got   [][]ContactName
	err   error
	wrote chan struct{}
}

func (f *fakeSink) put(_ context.Context, names []ContactName) (int, error) {
	f.mu.Lock()
	f.got = append(f.got, names)
	err := f.err
	f.mu.Unlock()
	if f.wrote != nil {
		f.wrote <- struct{}{}
	}
	return len(names), err
}
func (f *fakeSink) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.got) }

func oneContact() map[types.JID]types.ContactInfo {
	return map[types.JID]types.ContactInfo{jid("1", types.DefaultUserServer): {Found: true, FullName: "Layla"}}
}

// manualWait is a debounce timer the test releases: entered says the syncer is waiting, release lets it go.
type manualWait struct {
	entered chan time.Duration
	release chan struct{}
}

func newManualWait() *manualWait {
	return &manualWait{entered: make(chan time.Duration, 100), release: make(chan struct{}, 100)}
}
func (m *manualWait) wait(ctx context.Context, d time.Duration) error {
	m.entered <- d
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func runSyncer(t *testing.T, s *ContactSyncer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	return func() {
		cancel()
		waitFor(t, done, "Run to return")
	}
}

// Which events ask for a sync: connecting, the app-state patch that carries contacts, a contact change, a business
// name change. A push name does not (it is not a contact name), nor do other patches or messages.
func TestContactSyncTriggers(t *testing.T) {
	tests := []struct {
		name string
		evt  any
		want bool
	}{
		{"connected", &events.Connected{}, true},
		{"contacts patch", &events.AppStateSyncComplete{Name: appstate.WAPatchCriticalUnblockLow}, true},
		{"contacts patch, recovery", &events.AppStateSyncComplete{Name: appstate.WAPatchCriticalUnblockLow, Recovery: true}, true},
		{"other patch", &events.AppStateSyncComplete{Name: appstate.WAPatchRegular}, false},
		{"contact", &events.Contact{JID: jid("1", types.DefaultUserServer)}, true},
		{"business name", &events.BusinessName{JID: jid("1", types.DefaultUserServer), NewBusinessName: "Shop"}, true},
		{"push name", &events.PushName{JID: jid("1", types.DefaultUserServer), NewPushName: "lay"}, false},
		{"message", &events.Message{}, false},
		{"disconnected", &events.Disconnected{}, false},
		{"nil", nil, false},
		{"not an event", "Connected", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewContactSyncer(&fakeSource{}, (&fakeSink{}).put, slog.New(slog.DiscardHandler))
			s.Notify(tt.evt)
			if got := len(s.kick) == 1; got != tt.want {
				t.Errorf("Notify(%T) queued a sync = %v, want %v", tt.evt, got, tt.want)
			}
		})
	}
}

// Notify never blocks, however many events arrive while nothing runs.
func TestNotifyNeverBlocks(t *testing.T) {
	s := NewContactSyncer(&fakeSource{}, (&fakeSink{}).put, slog.New(slog.DiscardHandler))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			s.Notify(&events.Contact{})
		}
	}()
	waitFor(t, done, "1000 Notify calls")
}

// A burst of events waits out the debounce and makes one read and one write; an event that arrives while the sync
// runs makes exactly one more.
func TestContactSyncDebouncesAndCoalesces(t *testing.T) {
	src := &fakeSource{out: oneContact(), gate: make(chan struct{}), in: make(chan struct{}, 10)}
	sink := &fakeSink{wrote: make(chan struct{}, 10)}
	mw := newManualWait()
	s := NewContactSyncer(src, sink.put, slog.New(slog.DiscardHandler))
	s.wait = mw.wait
	stop := runSyncer(t, s)

	for range 10 {
		s.Notify(&events.Contact{})
	}
	if d := waitFor(t, mw.entered, "the debounce wait"); d != contactDebounce {
		t.Errorf("waited %v, want %v", d, contactDebounce)
	}
	if n := src.count(); n != 0 {
		t.Fatalf("source read %d times before the debounce ended", n)
	}
	s.Notify(&events.Connected{}) // still inside the window: joins the same sync
	mw.release <- struct{}{}
	waitFor(t, src.in, "the first read")

	s.Notify(&events.Contact{}) // arrives while the read runs: one more round
	close(src.gate)
	waitFor(t, sink.wrote, "the first write")
	waitFor(t, mw.entered, "the second debounce wait")
	mw.release <- struct{}{}
	waitFor(t, sink.wrote, "the second write")
	stop()

	if src.count() != 2 || sink.calls() != 2 {
		t.Errorf("reads = %d, writes = %d, want 2 and 2", src.count(), sink.calls())
	}
	want := []ContactName{{"1@s.whatsapp.net", "Layla"}}
	if !reflect.DeepEqual(sink.got[0], want) {
		t.Errorf("written = %v, want %v", sink.got[0], want)
	}
}

// A request that arrives during the debounce is served by the sync that follows it: when the source is read, no
// request is left queued, so it does not cause a second sync.
func TestContactSyncServesTheWindowsRequestsInOneSync(t *testing.T) {
	sink := &fakeSink{wrote: make(chan struct{}, 10)}
	mw := newManualWait()
	src := &fakeSource{out: oneContact()}
	s := NewContactSyncer(src, sink.put, slog.New(slog.DiscardHandler))
	s.wait = mw.wait
	queuedAtRead := -1
	src.onCall = func() { queuedAtRead = len(s.kick) }
	stop := runSyncer(t, s)
	s.Notify(&events.Contact{})
	waitFor(t, mw.entered, "the debounce wait")
	s.Notify(&events.Connected{})
	s.Notify(&events.BusinessName{})
	mw.release <- struct{}{}
	waitFor(t, sink.wrote, "the write")
	stop()
	if queuedAtRead != 0 {
		t.Errorf("%d requests were still queued when the source was read, want 0", queuedAtRead)
	}
	if src.count() != 1 {
		t.Errorf("reads = %d, want 1", src.count())
	}
}

// Nothing is read until an event asks for it.
func TestContactSyncIdleUntilNotified(t *testing.T) {
	src := &fakeSource{out: oneContact()}
	mw := newManualWait()
	s := NewContactSyncer(src, (&fakeSink{}).put, slog.New(slog.DiscardHandler))
	s.wait = mw.wait
	stop := runSyncer(t, s)
	s.Notify(&events.PushName{})
	stop()
	if src.count() != 0 || len(mw.entered) != 0 {
		t.Errorf("reads = %d, waits = %d, want none", src.count(), len(mw.entered))
	}
}

// A source or a sink error is logged and the next event syncs again; an empty list is not written.
func TestContactSyncErrorsAreLoggedNotFatal(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil))
	src := &fakeSource{err: errors.New("source broke"), in: make(chan struct{}, 10)}
	sink := &fakeSink{wrote: make(chan struct{}, 10)}
	mw := newManualWait()
	s := NewContactSyncer(src, sink.put, log)
	s.wait = mw.wait
	stop := runSyncer(t, s)

	step := func() {
		s.Notify(&events.Contact{})
		waitFor(t, mw.entered, "wait")
		mw.release <- struct{}{}
	}
	step()
	waitFor(t, src.in, "failing read")
	src.mu.Lock()
	src.err, src.out = nil, map[types.JID]types.ContactInfo{jid("1", types.DefaultUserServer): {PushName: "push only"}}
	src.mu.Unlock()
	step()
	waitFor(t, src.in, "read without names")
	src.mu.Lock()
	src.out = oneContact()
	src.mu.Unlock()
	sink.mu.Lock()
	sink.err = errors.New("sink broke")
	sink.mu.Unlock()
	step()
	waitFor(t, src.in, "read")
	waitFor(t, sink.wrote, "failing write")
	sink.mu.Lock()
	sink.err = nil
	sink.mu.Unlock()
	step()
	waitFor(t, src.in, "last read")
	waitFor(t, sink.wrote, "good write")
	stop()

	if sink.calls() != 2 {
		t.Errorf("writes = %d, want 2 (the empty list is not written)", sink.calls())
	}
	mu.Lock()
	logged := buf.String()
	mu.Unlock()
	for _, want := range []string{"source broke", "sink broke"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, "Layla") {
		t.Errorf("a contact name reached the log:\n%s", logged)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// Run stops on ctx, idle, in the debounce wait, and in the middle of a read.
func TestContactSyncStopsOnCancel(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		s := NewContactSyncer(&fakeSource{}, (&fakeSink{}).put, slog.New(slog.DiscardHandler))
		runSyncer(t, s)()
	})
	t.Run("in the debounce", func(t *testing.T) {
		mw := newManualWait()
		src := &fakeSource{out: oneContact()}
		s := NewContactSyncer(src, (&fakeSink{}).put, slog.New(slog.DiscardHandler))
		s.wait = mw.wait
		stop := runSyncer(t, s)
		s.Notify(&events.Connected{})
		waitFor(t, mw.entered, "wait")
		stop()
		if src.count() != 0 {
			t.Errorf("read after cancel: %d", src.count())
		}
	})
	t.Run("in the read", func(t *testing.T) {
		src := &fakeSource{out: oneContact(), gate: make(chan struct{}), in: make(chan struct{}, 1)}
		sink := &fakeSink{}
		s := NewContactSyncer(src, sink.put, slog.New(slog.DiscardHandler))
		s.wait = func(context.Context, time.Duration) error { return nil }
		stop := runSyncer(t, s)
		s.Notify(&events.Connected{})
		waitFor(t, src.in, "read")
		stop()
		if sink.calls() != 0 {
			t.Errorf("wrote after a cancelled read: %d", sink.calls())
		}
	})
}

func TestContactSyncWithDebounce(t *testing.T) {
	mw := newManualWait()
	s := NewContactSyncer(&fakeSource{}, (&fakeSink{}).put, nil).WithDebounce(7 * time.Second)
	s.wait = mw.wait
	stop := runSyncer(t, s)
	s.Notify(&events.Connected{})
	if d := waitFor(t, mw.entered, "wait"); d != 7*time.Second {
		t.Errorf("waited %v, want 7s", d)
	}
	stop()
}

func TestContactDebounceIsSeconds(t *testing.T) {
	if contactDebounce < time.Second || contactDebounce > time.Minute {
		t.Errorf("contactDebounce = %v", contactDebounce)
	}
	s := NewContactSyncer(&fakeSource{}, (&fakeSink{}).put, nil)
	if s.debounce != contactDebounce || s.wait == nil || s.log == nil {
		t.Errorf("defaults: debounce %v, wait nil=%v, log nil=%v", s.debounce, s.wait == nil, s.log == nil)
	}
}

// fakeContactStore is a ContactStore that answers only the read.
type fakeContactStore struct {
	wmstore.ContactStore
	src *fakeSource
}

func (f fakeContactStore) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	return f.src.GetAllContacts(ctx)
}

// The client hands out its own contact store, and nothing else, as the source.
func TestClientContactNamesIsTheDeviceContactStore(t *testing.T) {
	src := &fakeSource{}
	dev := &wmstore.Device{}
	dev.Contacts = fakeContactStore{src: src}
	c := &Client{client: &whatsmeow.Client{Store: dev}}
	got := c.ContactNames()
	if got == nil {
		t.Fatal("no source")
	}
	if _, err := got.GetAllContacts(context.Background()); err != nil || src.count() != 1 {
		t.Errorf("err = %v, calls = %d: the source is not the device's contact store", err, src.count())
	}
}

// Against whatsmeow's real SQL contact store in a temporary session.db: the names the app-state sync puts there
// (PutContactName(first, full), appstate.go:297) come back as FirstName and FullName, and ContactNames picks from them.
func TestContactNamesFromTheRealContactStore(t *testing.T) {
	ctx := t.Context()
	container, err := sqlstore.New(ctx, "sqlite", sessionDSN(filepath.Join(t.TempDir(), "session.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	d := container.NewDevice()
	own := types.JID{User: "15550001111", Device: 7, Server: types.DefaultUserServer}
	d.ID = &own
	d.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err = container.PutDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	dev, err := container.GetDevice(ctx, own)
	if err != nil || dev == nil {
		t.Fatalf("GetDevice: %v %v", dev, err)
	}
	c := &Client{client: &whatsmeow.Client{Store: dev}}
	layla, omar, shop := jid("201", types.DefaultUserServer), jid("202", types.DefaultUserServer), jid("203", types.HiddenUserServer)
	for _, e := range []struct {
		j           types.JID
		first, full string
	}{{layla, "Layla", "Layla Khan"}, {omar, "Omar", ""}, {shop, "", ""}} {
		if err = dev.Contacts.PutContactName(ctx, e.j, e.first, e.full); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = dev.Contacts.PutBusinessName(ctx, shop, "Corner Shop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = dev.Contacts.PutPushName(ctx, omar, "should-not-be-used"); err != nil {
		t.Fatal(err)
	}
	source := c.ContactNames()
	if source == nil {
		t.Fatal("no source")
	}
	all, err := source.GetAllContacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ContactName{{"201@s.whatsapp.net", "Layla Khan"}, {"202@s.whatsapp.net", "Omar"}, {"203@lid", "Corner Shop"}}
	if got := ContactNames(all); !reflect.DeepEqual(got, want) {
		t.Errorf("ContactNames = %v\nwant %v", got, want)
	}
}
