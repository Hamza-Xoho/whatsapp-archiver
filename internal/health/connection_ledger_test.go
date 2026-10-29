package health

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestConnectionStatesWriteOutageLedgerAndBacklogCounts(t *testing.T) {
	c := &clock{time.Unix(100, 0).UTC()}
	ledger := &connectionLedgerFake{}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, c, nil, func() string { return "v" }).WithConnectionLedger(ledger)

	m.SetState("connected", "")
	m.SetState("degraded", "keepalive timeout (failures=1)")
	c.now = time.Unix(160, 0).UTC()
	m.SetState("reconnecting", "reconnect_failed{class=dns,count=2}")
	c.now = time.Unix(220, 0).UTC()
	m.SetState("connected", "")
	m.OfflineSyncPreview(17)
	m.OfflineSyncCompleted(15)

	if len(ledger.periods) != 2 {
		t.Fatalf("periods=%+v", ledger.periods)
	}
	if got := ledger.periods[0]; got.State != "degraded" || got.CauseClass != "keepalive" || got.StartedAt != 100 {
		t.Fatalf("degraded period=%+v", got)
	}
	if got := ledger.periods[1]; got.State != "reconnecting" || got.CauseClass != "dns" || got.StartedAt != 160 || got.LastError != "reconnect_failed{class=dns,count=2}" {
		t.Fatalf("reconnecting period=%+v", got)
	}
	if len(ledger.closed) != 1 || ledger.closed[0] != 220 {
		t.Fatalf("closed=%v", ledger.closed)
	}
	if ledger.preview != 17 || ledger.completed != 15 {
		t.Fatalf("preview=%d completed=%d", ledger.preview, ledger.completed)
	}
}

func TestConcurrentConnectionTransitionsKeepLedgerOrder(t *testing.T) {
	c := &clock{time.Unix(100, 0).UTC()}
	ledger := &blockingConnectionLedger{
		opened:  make(chan struct{}),
		release: make(chan struct{}),
	}
	m := NewMonitor(&config.Config{DataDir: t.TempDir()}, nil, c, nil, func() string { return "v" }).WithConnectionLedger(ledger)
	m.SetState("connected", "")

	reconnectingDone := make(chan struct{})
	go func() {
		m.SetState("reconnecting", "reconnect_failed{class=tcp,count=1}")
		close(reconnectingDone)
	}()
	<-ledger.opened
	connectedDone := make(chan struct{})
	go func() {
		m.SetState("connected", "")
		close(connectedDone)
	}()
	select {
	case <-connectedDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(ledger.release)
	<-reconnectingDone
	<-connectedDone

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if !slices.Equal(ledger.operations, []string{"open", "close"}) {
		t.Fatalf("ledger operations=%v, want transition order [open close]", ledger.operations)
	}
}

type blockingConnectionLedger struct {
	mu         sync.Mutex
	opened     chan struct{}
	release    chan struct{}
	operations []string
}

func (f *blockingConnectionLedger) OpenConnectionPeriod(context.Context, store.ConnectionPeriod) (int64, error) {
	close(f.opened)
	<-f.release
	f.mu.Lock()
	f.operations = append(f.operations, "open")
	f.mu.Unlock()
	return 1, nil
}

func (f *blockingConnectionLedger) CloseConnectionPeriod(context.Context, int64) (bool, error) {
	f.mu.Lock()
	f.operations = append(f.operations, "close")
	f.mu.Unlock()
	return true, nil
}

func (f *blockingConnectionLedger) NoteConnectionError(context.Context, string) error { return nil }
func (f *blockingConnectionLedger) NoteOfflineSync(context.Context, *int64, *int64) error {
	return nil
}

type connectionLedgerFake struct {
	periods            []store.ConnectionPeriod
	closed             []int64
	preview, completed int64
}

func (f *connectionLedgerFake) OpenConnectionPeriod(_ context.Context, p store.ConnectionPeriod) (int64, error) {
	f.periods = append(f.periods, p)
	return int64(len(f.periods)), nil
}
func (f *connectionLedgerFake) CloseConnectionPeriod(_ context.Context, at int64) (bool, error) {
	f.closed = append(f.closed, at)
	return true, nil
}
func (f *connectionLedgerFake) NoteConnectionError(context.Context, string) error { return nil }
func (f *connectionLedgerFake) NoteOfflineSync(_ context.Context, preview, completed *int64) error {
	if preview != nil {
		f.preview = *preview
	}
	if completed != nil {
		f.completed = *completed
	}
	return nil
}
