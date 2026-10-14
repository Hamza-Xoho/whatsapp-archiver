package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/guard"
	"whatsapp-archiver/internal/retry"
	"whatsapp-archiver/internal/store"
)

type Writer interface {
	InsertMessage(context.Context, store.Message) (bool, error)
	InsertEvent(context.Context, store.MessageEvent) (bool, error)
	InsertChatEvent(context.Context, store.ChatEvent) (bool, error)
	InsertIDMapping(context.Context, store.IDMapping) (bool, error)
	UpsertChat(context.Context, store.Chat) error
	UpsertParticipant(context.Context, store.Participant) error
}
type MediaQueue interface {
	Enqueue(context.Context, store.Media) error
	OnRetry(context.Context, *events.MediaRetry)
}
type HealthSink interface {
	Seen(time.Time, bool)
	SetState(string, string)
	WriteFailed(error)
	Panic(string)
}
type Pipeline struct {
	writer Writer
	filter *Filter
	queue  MediaQueue
	health HealthSink
	log    *slog.Logger
	now    func() time.Time
	sleep  func(context.Context, time.Duration)
}

const (
	writeAttempts = 5
	writeBase     = 10 * time.Millisecond
	// writeMaxWait is the longest interval of the retry policy; after the policy gives up the write is retried at this pace.
	writeMaxWait = writeBase << (writeAttempts - 1)
)

func NewPipeline(w Writer, f *Filter, q MediaQueue, h HealthSink, log *slog.Logger) *Pipeline {
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{writer: w, filter: f, queue: q, health: h, log: log, now: time.Now, sleep: sleepContext}
}

// WithClock replaces the time source and the back-off sleep; tests use it to run retries on a fake clock.
func (p *Pipeline) WithClock(now func() time.Time, sleep func(time.Duration)) *Pipeline {
	p.now = now
	p.sleep = func(_ context.Context, d time.Duration) { sleep(d) }
	return p
}

func isPermanent(err error) bool { return errors.Is(err, store.ErrConstraint) }

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// persist runs one write so that it is never dropped: it retries with back-off, reports WriteFailed once when the
// policy gives up, then keeps retrying at the longest interval until the write succeeds or ctx ends.
func (p *Pipeline) persist(ctx context.Context, what string, fn func() error) error {
	policy := retry.Policy{Max: writeAttempts, Base: writeBase, Sleep: func(d time.Duration) { p.sleep(ctx, d) }, Permanent: isPermanent}
	_, err := retry.Do(ctx, policy, fn)
	if err == nil {
		return nil
	}
	p.health.WriteFailed(err)
	if isPermanent(err) {
		err = fmt.Errorf("%s: %w", what, err)
		p.log.Error("write rejected, not retrying", "what", what, "err", err)
		return err
	}
	for ctx.Err() == nil {
		p.sleep(ctx, writeMaxWait)
		if err = fn(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (p *Pipeline) Handle(evt any) {
	guard.Run(p.log, "handler", func(string, any) { p.health.Panic("handler") }, func() {
		ctx := context.Background()
		now := p.now()
		logErr := func(err error) {
			if err != nil {
				p.log.Error("event not stored", "type", fmt.Sprintf("%T", evt), "err", err)
			}
		}
		switch e := evt.(type) {
		case *events.Message:
			logErr(p.Ingest(ctx, FromMessage(e, "live", now)))
		case *events.UndecryptableMessage:
			logErr(p.Ingest(ctx, FromUndecryptable(e, now)))
		case *events.HistorySync:
			_, _, err := p.ImportHistory(ctx, e)
			logErr(err)
		case *events.GroupInfo, *events.JoinedGroup, *events.Archive, *events.Mute, *events.Pin:
			if r, ok := FromChatEvent(e, now); ok {
				logErr(p.Ingest(ctx, r))
			}
		case *events.MediaRetry:
			p.queue.OnRetry(ctx, e)
		case *events.Connected:
			p.health.SetState("connected", "")
		case *events.Disconnected:
			p.health.SetState("disconnected", "")
		case *events.LoggedOut:
			p.health.SetState("logged_out", fmt.Sprint(e.Reason))
		case *events.ClientOutdated:
			p.health.SetState("client_outdated", "")
		case *events.TemporaryBan:
			p.health.SetState("temp_banned", fmt.Sprint(e.Expire))
		case *events.StreamReplaced:
			p.health.SetState("stream_replaced", "")
		default:
			p.log.Debug("ignored event", "type", fmt.Sprintf("%T", evt))
		}
	})
}

func (p *Pipeline) Ingest(ctx context.Context, r Result) error {
	for _, m := range r.Mappings {
		if err := p.persist(ctx, "insert id mapping", func() error {
			_, err := p.writer.InsertIDMapping(ctx, m)
			return err
		}); err != nil {
			return err
		}
	}
	if !p.filter.Keep(r.Chat.JID, r.Kind) {
		return nil
	}
	write := func() error {
		if r.Chat.JID != "" {
			if err := p.writer.UpsertChat(ctx, r.Chat); err != nil {
				return err
			}
		}
		if r.Sender.JID != "" {
			if err := p.writer.UpsertParticipant(ctx, r.Sender); err != nil {
				return err
			}
		}
		if r.Message != nil {
			_, err := p.writer.InsertMessage(ctx, *r.Message)
			return err
		}
		if r.Event != nil {
			_, err := p.writer.InsertEvent(ctx, *r.Event)
			return err
		}
		if r.ChatEvent != nil {
			_, err := p.writer.InsertChatEvent(ctx, *r.ChatEvent)
			return err
		}
		return nil
	}
	if err := p.persist(ctx, fmt.Sprintf("write %s in %s", r.Kind, r.Chat.JID), write); err != nil {
		return err
	}
	if r.Media != nil {
		if err := p.persist(ctx, fmt.Sprintf("enqueue media in %s", r.Chat.JID), func() error { return p.queue.Enqueue(ctx, *r.Media) }); err != nil {
			return err
		}
	}
	p.health.Seen(r.Received, r.FromMe)
	return nil
}
