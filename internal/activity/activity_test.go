package activity

import (
	"fmt"
	"testing"
	"time"
)

func ev(i int) Event {
	return Event{At: time.Unix(int64(i), 0), Kind: "message", Chat: "g@g.us", Detail: fmt.Sprint(i)}
}

// NFR-2: capture never waits on a subscriber.
func TestSlowSubscriberNeverBlocksPublish(t *testing.T) {
	b := NewBus(500)
	ch, _ := b.Subscribe(4) // no deferred cancel: if Publish blocked it holds the lock and cancel would hang the failure
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10_000; i++ {
			b.Publish(ev(i))
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a subscriber that never reads")
	}
	n := 0
	for range ch { // the dropped subscriber's channel is closed, so the range ends
		n++
	}
	if n > 4 {
		t.Fatalf("a buffer of 4 delivered %d events", n)
	}
}

func TestDroppedSubscriberDoesNotAffectOthers(t *testing.T) {
	b := NewBus(500)
	b.Subscribe(1) // never read; no deferred cancels, which would hang a failure if Publish held the lock
	fast, _ := b.Subscribe(100)
	published := make(chan struct{})
	go func() {
		defer close(published)
		for i := 0; i < 50; i++ {
			b.Publish(ev(i))
		}
	}()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on the slow subscriber")
	}
	for i := 0; i < 50; i++ {
		select {
		case e := <-fast:
			if e.Detail != fmt.Sprint(i) {
				t.Fatalf("event %d out of order: %q", i, e.Detail)
			}
		case <-time.After(time.Second):
			t.Fatalf("fast subscriber missed event %d", i)
		}
	}
}

func TestRecentKeepsLast500(t *testing.T) {
	b := NewBus(500)
	for i := 0; i < 1200; i++ {
		b.Publish(ev(i))
	}
	got := b.Recent()
	if len(got) != 500 || got[0].Detail != "700" || got[499].Detail != "1199" {
		t.Fatalf("len=%d first=%q last=%q", len(got), got[0].Detail, got[len(got)-1].Detail)
	}
}

func TestRecentBeforeTheRingFillsIsOldestFirst(t *testing.T) {
	b := NewBus(500)
	for i := 0; i < 3; i++ {
		b.Publish(ev(i))
	}
	got := b.Recent()
	if len(got) != 3 || got[0].Detail != "0" || got[2].Detail != "2" {
		t.Fatalf("recent=%+v", got)
	}
}

func TestRecentIsACopy(t *testing.T) {
	b := NewBus(500)
	b.Publish(ev(1))
	b.Recent()[0].Detail = "changed"
	if got := b.Recent()[0].Detail; got != "1" {
		t.Fatalf("a caller changed the ring: %q", got)
	}
}

func TestNilBusIsNoop(t *testing.T) {
	var b *Bus
	b.Publish(ev(1))
	if got := b.Recent(); len(got) != 0 {
		t.Fatalf("recent=%v", got)
	}
	ch, cancel := b.Subscribe(4)
	cancel()
	if _, open := <-ch; open {
		t.Fatal("a nil bus gave an open channel")
	}
}

func TestSubscriberGetsEventsInOrderUntilCancelled(t *testing.T) {
	b := NewBus(500)
	ch, cancel := b.Subscribe(8)
	b.Publish(ev(1))
	b.Publish(ev(2))
	for _, want := range []string{"1", "2"} {
		if got := (<-ch).Detail; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	cancel()
	cancel() // idempotent
	if _, open := <-ch; open {
		t.Fatal("channel still open after cancel")
	}
	b.Publish(ev(3)) // after cancel: must not panic on a closed channel
}

func TestPublishStampsAMissingTime(t *testing.T) {
	b := NewBus(500)
	b.Publish(Event{Kind: "state"})
	if b.Recent()[0].At.IsZero() {
		t.Fatal("event kept a zero time")
	}
}
