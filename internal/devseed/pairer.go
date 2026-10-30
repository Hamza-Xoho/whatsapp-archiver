package devseed

import (
	"context"
	"sync"
	"time"

	"whatsapp-archiver/internal/web"
)

// devPairer is wired only by devseed's in-process preview server. It never imports or calls a WhatsApp client.
type devPairer struct {
	root    context.Context
	mu      sync.Mutex
	paired  bool
	started time.Time
	active  *devPairRun
}

type devPairRun struct {
	updates chan web.PairUpdate
	cancel  context.CancelFunc
}

func newDevPairer(root context.Context) *devPairer { return &devPairer{root: root} }

func (p *devPairer) Paired() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired
}

func (p *devPairer) PairingStarted() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started
}

func (p *devPairer) StartPairing(context.Context) (<-chan web.PairUpdate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		return p.active.updates, nil
	}
	return p.startLocked(), nil
}

func (p *devPairer) RePair(context.Context) (<-chan web.PairUpdate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		p.active.cancel()
	}
	p.paired = false
	return p.startLocked(), nil
}

func (p *devPairer) CancelPairing() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		p.active.cancel()
	}
	return nil
}

func (p *devPairer) startLocked() <-chan web.PairUpdate {
	ctx, cancel := context.WithCancel(p.root)
	run := &devPairRun{updates: make(chan web.PairUpdate, 2), cancel: cancel}
	p.active, p.started = run, time.Now()
	go p.run(ctx, run)
	return run.updates
}

func (p *devPairer) run(ctx context.Context, run *devPairRun) {
	defer func() {
		close(run.updates)
		p.mu.Lock()
		if p.active == run {
			p.active = nil
		}
		p.mu.Unlock()
	}()
	if !sendDevPairUpdate(ctx, run.updates, web.PairUpdate{QR: "synthetic-devseed-qr-1"}) {
		return
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if !sendDevPairUpdate(ctx, run.updates, web.PairUpdate{QR: "synthetic-devseed-qr-2"}) {
		return
	}
	timer.Reset(2 * time.Second)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		p.mu.Lock()
		p.paired = true
		p.mu.Unlock()
		_ = sendDevPairUpdate(ctx, run.updates, web.PairUpdate{Done: true})
	}
}

func sendDevPairUpdate(ctx context.Context, updates chan<- web.PairUpdate, update web.PairUpdate) bool {
	select {
	case updates <- update:
		return true
	case <-ctx.Done():
		return false
	}
}
