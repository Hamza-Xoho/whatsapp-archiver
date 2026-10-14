package wa

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestIsPairedReadsTheDevice(t *testing.T) {
	ctx := t.Context()
	missing := t.TempDir()
	if ok, err := IsPaired(ctx, missing); ok || err != nil {
		t.Fatalf("missing session: paired=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(missing, "session.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("IsPaired created session.db: %v", err)
	}

	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "session.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsPaired(ctx, empty); ok || err != nil {
		t.Fatalf("empty session: paired=%v err=%v", ok, err)
	}

	paired := t.TempDir()
	c, err := sqlstore.New(ctx, "sqlite", sessionDSN(filepath.Join(paired, "session.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	d := c.NewDevice()
	d.ID = &types.JID{User: "15550001111", Device: 7, Server: types.DefaultUserServer}
	d.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err := c.PutDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if ok, err := IsPaired(ctx, paired); !ok || err != nil {
		t.Fatalf("paired session: paired=%v err=%v", ok, err)
	}
}

func TestPairLine(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{&events.PairSuccess{}, "Paired. Capture is running."},
		{&events.PairError{Error: errors.New("bad adv")}, "Pairing failed: bad adv"},
		{whatsmeow.QRChannelTimeout, "QR code timed out; run `archiver pair` again."},
		{whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Error: errors.New("boom")}, "Pairing failed: boom"},
		{whatsmeow.QRChannelClientOutdated, "Pairing failed: err-client-outdated"},
		{whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "x"}, ""},
		{whatsmeow.QRChannelSuccess, ""},
		{&events.Connected{}, ""},
	}
	for _, tt := range tests {
		if got := pairLine(tt.in); got != tt.want {
			t.Errorf("pairLine(%#v)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestForwardControlEvents(t *testing.T) {
	forwarded := []any{
		&events.Connected{}, &events.ClientOutdated{}, &events.TemporaryBan{}, &events.LoggedOut{},
		&events.StreamReplaced{}, &events.ConnectFailure{}, &events.CATRefreshError{},
	}
	for _, evt := range forwarded {
		ctl := make(chan any, 1)
		forwardControl(ctl, evt)
		select {
		case got := <-ctl:
			if got != evt {
				t.Fatalf("%T: forwarded %v", evt, got)
			}
		default:
			t.Fatalf("%T not forwarded", evt)
		}
	}
	for _, evt := range []any{&events.Disconnected{}, &events.Message{}} {
		ctl := make(chan any, 1)
		forwardControl(ctl, evt)
		if len(ctl) != 0 {
			t.Fatalf("%T forwarded", evt)
		}
	}
	full := make(chan any) // no reader: forwarding must not block the event handler
	forwardControl(full, &events.Connected{})
}

func TestSleepCtxStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("sleepCtx did not return on cancel")
	}
}
