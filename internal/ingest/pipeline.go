package ingest

import (
	"context"
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
}

func (p *Pipeline) ImportHistory(context.Context, *events.HistorySync) (int, int, error) {
	return 0, 0, nil
}

func NewPipeline(w Writer, f *Filter, q MediaQueue, h HealthSink, log *slog.Logger) *Pipeline {
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{writer: w, filter: f, queue: q, health: h, log: log, now: time.Now}
}

func (p *Pipeline) Handle(evt any) {
	guard.Run(p.log, "handler", func(string, any) { p.health.Panic("handler") }, func() {
		ctx := context.Background()
		now := p.now()
		switch e := evt.(type) {
		case *events.Message:
			_ = p.Ingest(ctx, FromMessage(e, "live", now))
		case *events.UndecryptableMessage:
			_ = p.Ingest(ctx, FromUndecryptable(e, now))
		case *events.HistorySync:
			_, _, _ = p.ImportHistory(ctx, e)
		case *events.GroupInfo, *events.JoinedGroup, *events.Archive, *events.Mute, *events.Pin:
			if r, ok := FromChatEvent(e, now); ok {
				_ = p.Ingest(ctx, r)
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
		if _, err := p.writer.InsertIDMapping(ctx, m); err != nil {
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
	_, err := retry.Do(ctx, retry.Policy{Max: 5, Base: 10 * time.Millisecond}, write)
	if err != nil {
		p.health.WriteFailed(err)
		for err != nil && ctx.Err() == nil {
			_, err = retry.Do(ctx, retry.Policy{Max: 1, Base: 160 * time.Millisecond}, write)
		}
	}
	if err != nil {
		return err
	}
	if r.Media != nil {
		if err = p.queue.Enqueue(ctx, *r.Media); err != nil {
			return err
		}
	}
	p.health.Seen(r.Received, r.FromMe)
	return nil
}
