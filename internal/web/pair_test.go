package web

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/testkit"
)

// fakePairer is the wa pairer at the web boundary: StartPairing hands out one channel the test feeds.
type fakePairer struct {
	mu     sync.Mutex
	paired bool
	starts int
	ch     chan PairUpdate
	closed bool
}

func newFakePairer(t *testing.T, paired bool) *fakePairer {
	f := &fakePairer{paired: paired, ch: make(chan PairUpdate, 8)}
	t.Cleanup(f.end) // the producer closes the channel, so the page's reader stops
	return f
}

func (f *fakePairer) Paired() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.paired }
func (f *fakePairer) StartPairing(context.Context) (<-chan PairUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return f.ch, nil
}
func (f *fakePairer) startCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.starts }
func (f *fakePairer) send(u PairUpdate) {
	f.mu.Lock()
	if u.Done {
		f.paired = true
	}
	f.mu.Unlock()
	f.ch <- u
}
func (f *fakePairer) end() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
}

func newPairUI(t *testing.T, f *fakePairer) *Server {
	t.Helper()
	return New(Deps{Store: testkit.Store(t), Config: &config.Config{TimeZone: "UTC"}, Pairer: f, Activity: activity.NewBus(10)})
}

// samePost sends a same-origin form POST without htmx, as the pair page's button does.
func samePost(t *testing.T, h http.Handler, path string, origin bool) *http.Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "http://localhost:8080"+path, strings.NewReader(url.Values{}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "localhost:8080"
	if origin {
		r.Header.Set("Origin", "http://localhost:8080")
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

// waitPage polls GET /pair until its visible text holds want.
func waitPage(t *testing.T, h http.Handler, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, code, _ := get(t, h, "/pair")
		if code != http.StatusOK {
			t.Fatalf("GET /pair: %d", code)
		}
		if strings.Contains(visibleText(string(body)), want) || strings.Contains(string(body), want) {
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /pair never showed %q; last:\n%s", want, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPairPageWhenUnpairedHasButton(t *testing.T) {
	f := newFakePairer(t, false)
	body, code, _ := get(t, newPairUI(t, f).Handler(), "/pair")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	page := string(body)
	if !strings.Contains(page, `<form method="post" action="/pair/start">`) || !strings.Contains(visibleText(page), "Pair WhatsApp") {
		t.Fatalf("no Pair WhatsApp button:\n%s", page)
	}
	if f.startCount() != 0 {
		t.Fatal("viewing the page started pairing")
	}
}

func TestPairPageWhenPairedHasNoButton(t *testing.T) {
	f := newFakePairer(t, true)
	body, code, _ := get(t, newPairUI(t, f).Handler(), "/pair")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	page := visibleText(string(body))
	contains(t, page, "Paired", "history: none received yet")
	if strings.Contains(string(body), "/pair/start") || strings.Contains(page, "Pair WhatsApp") {
		t.Fatalf("paired page offers pairing:\n%s", body)
	}
}

// POST /pair/start starts pairing once; the page then shows the QR image, and after Done the success line and a
// link to the dashboard.
func TestPairStartShowsQRThenDone(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	resp := samePost(t, h, "/pair/start", true)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/pair" {
		t.Fatalf("POST /pair/start: %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if f.startCount() != 1 {
		t.Fatalf("starts=%d, want 1", f.startCount())
	}
	f.send(PairUpdate{QR: "2@first"})
	page := waitPage(t, h, `src="/pair/qr.png`)
	if strings.Contains(page, "2@first") {
		t.Fatal("the page carries the QR code text")
	}
	contains(t, visibleText(page), "Linked devices")
	f.send(PairUpdate{Done: true})
	page = waitPage(t, h, "Paired. Capture is running.")
	if !strings.Contains(page, `href="/"`) {
		t.Fatalf("no link to the dashboard:\n%s", page)
	}
	if strings.Contains(page, `src="/pair/qr.png`) {
		t.Fatal("QR still shown after pairing")
	}
}

func TestQRPNGIsValidImage(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	samePost(t, h, "/pair/start", true)
	f.send(PairUpdate{QR: "2@Zm9vYmFy,c2VjcmV0,a2V5,YWR2"})
	waitPage(t, h, `src="/pair/qr.png`)
	body, code, ctype := get(t, h, "/pair/qr.png")
	if code != http.StatusOK || ctype != "image/png" {
		t.Fatalf("GET /pair/qr.png: %d %q", code, ctype)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 21 || b.Dx() != b.Dy() {
		t.Fatalf("QR image bounds %v", b)
	}
}

// GET /pair/qr.png has no side effect: without a code it is 404, and it never starts pairing.
func TestQRPNGWithoutCodeIs404AndStartsNothing(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	if _, code, _ := get(t, h, "/pair/qr.png"); code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
	if f.startCount() != 0 {
		t.Fatal("GET /pair/qr.png started pairing")
	}
}

func TestPairStartNeedsSameOrigin(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	if resp := samePost(t, h, "/pair/start", false); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
	if f.startCount() != 0 {
		t.Fatal("a cross-site POST started pairing")
	}
}

func TestPairStartWhenPairedStartsNothing(t *testing.T) {
	f := newFakePairer(t, true)
	h := newPairUI(t, f).Handler()
	if resp := samePost(t, h, "/pair/start", true); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if f.startCount() != 0 {
		t.Fatal("POST /pair/start on a paired device started pairing")
	}
}

// A failed pairing shows why, and the button again.
func TestPairErrorShowsReasonAndButton(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	samePost(t, h, "/pair/start", true)
	f.send(PairUpdate{QR: "2@x"})
	waitPage(t, h, `src="/pair/qr.png`)
	f.send(PairUpdate{Err: "QR code timed out; run `archiver pair` again."})
	page := waitPage(t, h, "QR code timed out")
	if !strings.Contains(page, `action="/pair/start"`) || strings.Contains(page, `src="/pair/qr.png`) {
		t.Fatalf("failed pairing page:\n%s", page)
	}
	if _, code, _ := get(t, h, "/pair/qr.png"); code != http.StatusNotFound {
		t.Fatalf("QR still served after the failure: %d", code)
	}
}

// Each pairing step reaches open pages as an SSE "pair" event carrying the pair panel, never the code text.
func TestPairEventOnStream(t *testing.T) {
	f := newFakePairer(t, false)
	s := newPairUI(t, f)
	events := openStream(t, s, nil)
	next(t, events, "health", 5*time.Second)
	samePost(t, s.Handler(), "/pair/start", true)
	f.send(PairUpdate{QR: "2@streamed"})
	e := next(t, events, "pair", 5*time.Second)
	for !strings.Contains(e.data, `src="/pair/qr.png`) {
		e = next(t, events, "pair", 5*time.Second)
	}
	if strings.Contains(e.data, "2@streamed") {
		t.Fatal("pair event carries the QR code text")
	}
	f.send(PairUpdate{Done: true})
	e = next(t, events, "pair", 5*time.Second)
	for !strings.Contains(e.data, "Paired. Capture is running.") {
		e = next(t, events, "pair", 5*time.Second)
	}
}

// Once paired, the old QR code is no longer served.
func TestQRPNGGoneAfterDone(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	samePost(t, h, "/pair/start", true)
	f.send(PairUpdate{QR: "2@x"})
	waitPage(t, h, `src="/pair/qr.png`)
	f.send(PairUpdate{Done: true})
	waitPage(t, h, "Paired. Capture is running.")
	if _, code, _ := get(t, h, "/pair/qr.png"); code != http.StatusNotFound {
		t.Fatalf("QR served after pairing: %d", code)
	}
}

// Every new QR code reaches an open page as a pair event with a new image URL.
func TestEachNewQRCodeIsStreamed(t *testing.T) {
	f := newFakePairer(t, false)
	s := newPairUI(t, f)
	h := s.Handler()
	samePost(t, h, "/pair/start", true)
	f.send(PairUpdate{QR: "2@one"})
	waitPage(t, h, `src="/pair/qr.png?n=1"`)
	events := openStream(t, s, nil) // connects while a pairing runs: the panel is sent at once
	e := next(t, events, "pair", 5*time.Second)
	if !strings.Contains(e.data, `src="/pair/qr.png?n=1"`) {
		t.Fatalf("pair event on connect: %q", e.data)
	}
	f.send(PairUpdate{QR: "2@two"})
	if e := next(t, events, "pair", 5*time.Second); !strings.Contains(e.data, `src="/pair/qr.png?n=2"`) {
		t.Fatalf("pair event after the second code: %q", e.data)
	}
}

// A second POST during the same pairing (the Pairer returns the same channel) keeps the shown code and starts no
// second reader.
func TestSecondPairStartKeepsTheRunningPairing(t *testing.T) {
	f := newFakePairer(t, false)
	h := newPairUI(t, f).Handler()
	samePost(t, h, "/pair/start", true)
	f.send(PairUpdate{QR: "2@one"})
	waitPage(t, h, `src="/pair/qr.png?n=1"`)
	samePost(t, h, "/pair/start", true)
	if _, code, _ := get(t, h, "/pair/qr.png"); code != http.StatusOK {
		t.Fatalf("second start reset the pairing: QR status %d", code)
	}
	body, _, _ := get(t, h, "/pair")
	if !strings.Contains(string(body), `src="/pair/qr.png?n=1"`) {
		t.Fatalf("second start changed the page:\n%s", body)
	}
}
