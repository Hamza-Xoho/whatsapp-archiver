package main

import (
	"context"
	"sync"

	"whatsapp-archiver/internal/wa"
	"whatsapp-archiver/internal/web"
)

// waPairer is what the adapter needs from *wa.Pairer.
type waPairer interface {
	Paired() bool
	StartPairing(ctx context.Context) (<-chan wa.PairUpdate, error)
}

// browserPairer adapts wa.Pairer to web.Pairer, so the web package never imports wa (nor whatsmeow). Updates are
// copied field for field; a second StartPairing during the same pairing returns the same channel, as wa does.
type browserPairer struct {
	p   waPairer
	mu  sync.Mutex
	src <-chan wa.PairUpdate
	out <-chan web.PairUpdate
}

func (b *browserPairer) Paired() bool { return b.p.Paired() }

func (b *browserPairer) StartPairing(ctx context.Context) (<-chan web.PairUpdate, error) {
	src, err := b.p.StartPairing(ctx)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if src == b.src {
		return b.out, nil
	}
	out := make(chan web.PairUpdate, cap(src))
	b.src, b.out = src, out
	go forwardPair(src, out) // ends when wa closes src: pairing ended or the archiver stopped
	return out, nil
}

// browserQRTimedOut replaces the terminal's timeout line in the browser. The terminal line says to run
// `archiver pair`, which next to a running service would be a second process on the same archive and session.
const browserQRTimedOut = "QR code timed out. Press Pair WhatsApp to try again."

// forwardPair copies each update, with the browser's own timeout text, and closes out after src.
func forwardPair(src <-chan wa.PairUpdate, out chan<- web.PairUpdate) {
	defer close(out)
	for u := range src {
		if u.Err == wa.QRTimedOut {
			u.Err = browserQRTimedOut
		}
		out <- web.PairUpdate(u)
	}
}
