package devseed

import (
	"context"
	"sync"
	"time"

	"whatsapp-archiver/internal/web"
)

// devPairer is wired only by devseed's in-process preview server. It never imports or calls a WhatsApp client.
// Its deterministic attempts let the evaluation UI exercise failure, success, logout, re-pair and cancellation.
type devPairer struct {
	root    context.Context
	timing  devPairTiming
	mu      sync.Mutex
	paired  bool
	state   string
	started time.Time
	attempt int
	active  *devPairRun
}

type devPairTiming struct {
	Waiting, QR, Finish, LoggedOutDelay time.Duration
}

var browserPairTiming = devPairTiming{
	Waiting:        250 * time.Millisecond,
	QR:             2 * time.Second,
	Finish:         1500 * time.Millisecond,
	LoggedOutDelay: 3 * time.Second,
}

type devPairRun struct {
	updates chan web.PairUpdate
	ctx     context.Context
	cancel  context.CancelFunc
}

func newDevPairer(root context.Context) *devPairer {
	return newDevPairerWithTiming(root, browserPairTiming)
}

func newDevPairerWithTiming(root context.Context, timing devPairTiming) *devPairer {
	return &devPairer{root: root, timing: timing, state: "not_paired"}
}

func (p *devPairer) Paired() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired
}

func (p *devPairer) HealthState() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
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
	p.stopLocked(false)
	p.paired = false
	return p.startLocked(), nil
}

func (p *devPairer) CancelPairing() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked(true)
	return nil
}

// stopLocked detaches before cancellation, so an immediate Start cannot receive the cancelled run's channel.
func (p *devPairer) stopLocked(report bool) {
	if p.active == nil {
		return
	}
	run := p.active
	p.active = nil
	if report {
		select {
		case run.updates <- web.PairUpdate{Err: "Pairing cancelled."}:
		default:
		}
		p.state = "not_paired"
	}
	run.cancel()
}

func (p *devPairer) startLocked() <-chan web.PairUpdate {
	ctx, cancel := context.WithCancel(p.root)
	run := &devPairRun{updates: make(chan web.PairUpdate, 2), ctx: ctx, cancel: cancel}
	p.attempt++
	attempt := p.attempt
	p.active, p.started, p.state = run, time.Now(), "starting"
	go p.run(ctx, run, attempt)
	return run.updates
}

func (p *devPairer) run(ctx context.Context, run *devPairRun, attempt int) {
	defer func() {
		close(run.updates)
		p.mu.Lock()
		if p.active == run {
			p.active = nil
		}
		p.mu.Unlock()
	}()
	if !waitDevPair(ctx, p.timing.Waiting) {
		return
	}
	if !p.send(ctx, run, web.PairUpdate{QR: "synthetic-devseed-qr-1"}, "pairing") {
		return
	}
	if attempt == 1 {
		if !waitDevPair(ctx, p.timing.QR) || !p.send(ctx, run, web.PairUpdate{QR: "synthetic-devseed-qr-2"}, "pairing") || !waitDevPair(ctx, p.timing.Finish) {
			return
		}
		if !p.setStateForRun(run, "not_paired", false) {
			return
		}
		_ = sendDevPairUpdate(ctx, run.updates, web.PairUpdate{Err: "synthetic devseed pairing failure"})
		return
	}
	if !waitDevPair(ctx, p.timing.Finish) {
		return
	}
	if !p.setStateForRun(run, "connected", true) {
		return
	}
	if !sendDevPairUpdate(ctx, run.updates, web.PairUpdate{Done: true}) {
		return
	}
	if attempt == 2 && waitDevPair(ctx, p.timing.LoggedOutDelay) {
		p.setStateForRun(run, "logged_out", false)
	}
}

func (p *devPairer) send(ctx context.Context, run *devPairRun, update web.PairUpdate, state string) bool {
	if !p.setStateForRun(run, state, false) {
		return false
	}
	return sendDevPairUpdate(ctx, run.updates, update)
}

func (p *devPairer) setStateForRun(run *devPairRun, state string, paired bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != run {
		return false
	}
	select {
	case <-run.ctx.Done():
		return false
	default:
	}
	p.state, p.paired = state, paired
	return true
}

func waitDevPair(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
