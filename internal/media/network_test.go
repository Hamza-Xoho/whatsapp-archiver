package media

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestNetworkFailuresDoNotConsumeAttempts(t *testing.T) {
	st := testkit.Store(t)
	clock := newClock()
	d := &fakeDown{download: func([]byte, string) ([]byte, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "mmg.whatsapp.net"}
	}}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, clock, nil)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "NET", Type: "image", RawProto: []byte("raw"), Attempts: 19})
	stop := start(t, q)
	eventually(t, "network failure rescheduled", func() bool {
		m := row(t, st, "g@g.us", "NET")
		return m.State == "pending" && m.ErrorClass == "network"
	})
	stop()

	m := row(t, st, "g@g.us", "NET")
	if m.Attempts != 19 {
		t.Fatalf("attempts = %d, want 19", m.Attempts)
	}
	if m.NextAttemptAt != clock.Now().Add(15*time.Minute).Unix() {
		t.Fatalf("next attempt = %d, want a 15 minute capped backoff", m.NextAttemptAt)
	}
}

func TestQueuePausesWhileDisconnected(t *testing.T) {
	st := testkit.Store(t)
	d := &fakeDown{}
	q := newTestQueue(t, st, d, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	var connected atomic.Bool
	q.WithConnected(connected.Load)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "WAIT", Type: "image", RawProto: []byte("raw")})
	stop := start(t, q)
	time.Sleep(250 * time.Millisecond)
	if calls := d.calls(); len(calls) != 0 {
		t.Fatalf("downloads while disconnected = %v", calls)
	}
	if m := row(t, st, "g@g.us", "WAIT"); m.State != "pending" {
		t.Fatalf("state while disconnected = %q, want pending", m.State)
	}
	connected.Store(true)
	q.Wake()
	eventually(t, "download after reconnect", func() bool { return row(t, st, "g@g.us", "WAIT").State == "done" })
	stop()
}
