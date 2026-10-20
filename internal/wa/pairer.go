package wa

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow"
)

// PairUpdate is one step of pairing from the browser, as plain strings: a QR code to show, the end of pairing, or
// why it failed. cmd/archiver converts it to web.PairUpdate, so the web package never imports this one.
type PairUpdate struct {
	QR   string
	Done bool
	Err  string
}

// pairUpdates is a pairing's channel buffer. Updates are sent without waiting, so a slow reader never holds up the
// QR channel (whatsmeow drops the whole pairing when its channel is full, qrchan.go:98-107). One connection yields at
// most six codes and one last update, so nothing is dropped even with no reader.
const pairUpdates = 16

// errPairingStopped is StartPairing's answer once Run has returned.
var errPairingStopped = errors.New("pairing is not available: the archiver is stopping")

// Pairer runs `archiver run`'s connection: capture at once when the device is paired, otherwise nothing until
// StartPairing, then pairing in the same run, carrying on as capture after PairSuccess. Only one pairing runs at a
// time; a failed one can be started again.
type Pairer struct {
	c         *Client
	start     chan struct{} // StartPairing's request to Run; buffered, at most one pending
	ready     chan struct{} // closed once paired: capture may start
	readyOnce sync.Once
	mu        sync.Mutex
	cur       chan PairUpdate // the running pairing's updates; nil when none runs
	stopped   bool            // Run has returned
}

// NewPairer wraps c. Run must be called for pairing to start.
func NewPairer(c *Client) *Pairer {
	return &Pairer{c: c, start: make(chan struct{}, 1), ready: make(chan struct{})}
}

// Paired reports whether the device holds a login.
func (p *Pairer) Paired() bool { return p.c.Paired() }

// Ready is closed once the device is paired: at Run's start when it already was, else at PairSuccess.
func (p *Pairer) Ready() <-chan struct{} { return p.ready }

// StartPairing asks Run to pair and returns the pairing's updates: each QR code, then Done or Err, then the channel
// is closed. While a pairing runs, a second call returns the same channel and starts nothing.
func (p *Pairer) StartPairing(context.Context) (<-chan PairUpdate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.stopped:
		return nil, errPairingStopped
	case p.c.Paired():
		return nil, errors.New("already paired")
	case p.cur != nil:
		return p.cur, nil
	}
	p.cur = make(chan PairUpdate, pairUpdates)
	select {
	case p.start <- struct{}{}:
	default: // a request is already pending
	}
	return p.cur, nil
}

// Run captures until ctx ends. Unpaired, it connects to nothing until StartPairing, then pairs; a failed pairing
// disconnects and waits for the next StartPairing. It returns nil once ctx ends.
func (p *Pairer) Run(ctx context.Context, h func(any) bool) error {
	defer p.stop()
	if p.c.Paired() {
		p.markReady()
		return p.c.Run(ctx, h, false)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.start:
		}
		p.mu.Lock()
		out := p.cur
		p.mu.Unlock()
		if out == nil {
			continue
		}
		actx, cancel := context.WithCancel(ctx)
		err := p.c.run(actx, h, true, func(ctx context.Context, qr <-chan whatsmeow.QRChannelItem) {
			p.forward(ctx, cancel, qr, out)
		})
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if p.isReady() {
			return err // capture ended by itself after pairing
		}
		if err != nil {
			p.end(out, &PairUpdate{Err: fmt.Sprintf("Pairing failed: %v", err)})
		} else {
			p.end(out, nil)
		}
	}
}

// forward turns the QR channel into updates: a code becomes QR, success becomes Done and marks the Pairer ready, an
// error or timeout becomes Err with pairLine's text and cancels the attempt. It returns when the channel closes or
// the attempt ends.
func (p *Pairer) forward(ctx context.Context, cancel context.CancelFunc, qr <-chan whatsmeow.QRChannelItem, out chan PairUpdate) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-qr:
			if !ok {
				if !p.isReady() {
					cancel()
				}
				return
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				p.send(out, PairUpdate{QR: item.Code})
			case whatsmeow.QRChannelSuccess.Event:
				p.c.setPaired(true) // the QR channel hears PairSuccess before run's handler does
				p.markReady()
				p.end(out, &PairUpdate{Done: true})
			default:
				if line := pairLine(item); line != "" {
					p.end(out, &PairUpdate{Err: line})
					cancel()
					return
				}
			}
		}
	}
}

// send delivers u if out is still the running pairing's channel, without waiting.
func (p *Pairer) send(out chan PairUpdate, u PairUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != out {
		return
	}
	select {
	case out <- u:
	default:
	}
}

// end sends the last update (nil for none) and closes out, once; a later StartPairing starts a new pairing.
func (p *Pairer) end(out chan PairUpdate, last *PairUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != out {
		return
	}
	if last != nil {
		select {
		case out <- *last:
		default:
		}
	}
	close(out)
	p.cur = nil
}

// stop ends a pairing still open when Run returns and refuses new ones.
func (p *Pairer) stop() {
	p.mu.Lock()
	out := p.cur
	p.stopped = true
	p.mu.Unlock()
	if out != nil {
		p.end(out, nil)
	}
}

func (p *Pairer) markReady() { p.readyOnce.Do(func() { close(p.ready) }) }

func (p *Pairer) isReady() bool {
	select {
	case <-p.ready:
		return true
	default:
		return false
	}
}
