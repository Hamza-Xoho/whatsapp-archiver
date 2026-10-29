package media

import (
	"sync/atomic"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestDueMediaDoesNotSpinStoreWhileDisconnected(t *testing.T) {
	st := testkit.Store(t)
	counted := &countingMediaStore{Store: st}
	q := newTestQueue(t, st, &fakeDown{}, config.MediaRules{Download: []string{"image"}, Workers: 1}, newClock(), nil)
	q.store = counted
	var connected atomic.Bool
	q.WithConnected(connected.Load)
	enqueue(t, q, store.Media{ChatJID: "g@g.us", MessageID: "DUE", Source: "live", Type: "image", RawProto: []byte("raw")})
	stop := start(t, q)
	time.Sleep(250 * time.Millisecond)
	if got := counted.calls.Load(); got > 3 {
		t.Fatalf("store calls while disconnected with due media = %d, want <= 3", got)
	}
	connected.Store(true)
	q.Wake()
	eventually(t, "due media after reconnect", func() bool { return row(t, st, "g@g.us", "DUE").State == "done" })
	stop()
}
