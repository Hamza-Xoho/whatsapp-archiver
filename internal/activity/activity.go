// Package activity is the in-memory activity bus: capture publishes small events and the dashboard's live feed
// subscribes. Publishing never waits: a subscriber whose buffer is full is dropped, not waited for (NFR-2).
// Events carry no message text, captions or URLs, only a chat JID and counts, states and types.
package activity

import (
	"sync"
	"time"
)

// Event is one line of the live feed. Kind is one of message, event, chat_event, history_chunk, media, state, backup,
// gap or excluded_skip. Chat is a JID (empty when the event has no chat). Detail holds counts, states and types
// such as "image done" or "chunk FULL #12 · 412 messages", never text, captions or URLs.
type Event struct {
	At     time.Time
	Kind   string
	Chat   string
	Detail string
}

// Bus is a ring of the last events plus the live subscribers. A nil *Bus is a no-op publisher.
type Bus struct {
	mu   sync.Mutex
	ring []Event // oldest first once full; next is where the next event goes
	next int
	full bool
	subs map[*subscriber]struct{}
}
type subscriber struct{ ch chan Event }

// NewBus keeps the last ring events (at least 1).
func NewBus(ring int) *Bus {
	if ring < 1 {
		ring = 1
	}
	return &Bus{ring: make([]Event, ring), subs: map[*subscriber]struct{}{}}
}

// Publish records e and offers it to every subscriber without ever blocking: a subscriber whose buffer is full is
// dropped and its channel closed. An event without a time is stamped with the current time.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring[b.next] = e
	b.next++
	if b.next == len(b.ring) {
		b.next, b.full = 0, true
	}
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

// Subscribe returns a channel of the events published from now on (buffer buf, at least 1) and a cancel that stops
// and closes it. The channel is also closed when the subscriber falls behind and is dropped; a reader must treat a
// closed channel as "reconnect". Cancel is safe to call more than once. On a nil bus the channel is already closed.
func (b *Bus) Subscribe(buf int) (<-chan Event, func()) {
	if buf < 1 {
		buf = 1
	}
	s := &subscriber{ch: make(chan Event, buf)}
	if b == nil {
		close(s.ch)
		return s.ch, func() {}
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
	}
}

// Recent returns a copy of the retained events, oldest first.
func (b *Bus) Recent() []Event {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		return append([]Event(nil), b.ring[:b.next]...)
	}
	return append(append([]Event(nil), b.ring[b.next:]...), b.ring[:b.next]...)
}

// RecentFiltered returns the newest matching events, oldest first. Filtering
// happens while the bus owns the ring so an older match is not discarded by a
// global limit. The returned slice is an independent copy.
func (b *Bus) RecentFiltered(chat, kind string, limit int) []Event {
	all := b.Recent()
	if limit < 1 {
		return nil
	}
	out := make([]Event, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if chat != "" && all[i].Chat != chat {
			continue
		}
		if kind != "" && all[i].Kind != kind {
			continue
		}
		out = append(out, all[i])
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}
