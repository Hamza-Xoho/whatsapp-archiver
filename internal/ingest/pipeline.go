package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/guard"
	"whatsapp-archiver/internal/retry"
	"whatsapp-archiver/internal/store"
)

// Writer is what ingest writes to. Apply stores everything one event writes in one transaction (R2); an id mapping is a
// separate, earlier transaction because the exclusion filter must see it before it decides.
type Writer interface {
	Apply(context.Context, store.Batch) (store.Applied, error)
	InsertIDMapping(context.Context, store.IDMapping) (bool, error)
}

// MediaQueue is the media workers as ingest sees them. Decide applies the media rules to a row about to be stored
// (state, reason, queue time); a failed rule read is an error and decides nothing. The row is then stored by Apply in
// the same transaction as its message, and Wake tells the workers it exists.
type MediaQueue interface {
	Decide(context.Context, *store.Media) error
	Wake()
	OnRetry(context.Context, *events.MediaRetry)
}
type HealthSink interface {
	Seen(time.Time, bool)
	SetState(string, string)
	WriteFailed(error)
	Panic(string)
}
type Pipeline struct {
	ctx    context.Context // the serve context: Handle's retry loops end with it
	writer Writer
	filter *Filter
	queue  MediaQueue
	health HealthSink
	log    *slog.Logger
	now    func() time.Time
	sleep  func(context.Context, time.Duration)
	parse  HistoryParser
	votes  VoteDecryptor // nil: live poll votes keep an empty value
	seen   *SeenTracker  // nil: first sightings are not recorded
	bus    *activity.Bus // nil: nothing is published
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
	return &Pipeline{ctx: context.Background(), writer: w, filter: f, queue: q, health: h, log: log, now: time.Now, sleep: sleepContext}
}

// WithClock replaces the time source and the back-off sleep; tests use it to run retries on a fake clock.
func (p *Pipeline) WithClock(now func() time.Time, sleep func(time.Duration)) *Pipeline {
	p.now = now
	p.sleep = func(_ context.Context, d time.Duration) { sleep(d) }
	return p
}

// WithContext sets the serve context. When it ends (SIGTERM), a write that is still failing stops being retried and
// that event is not stored, which is acceptable because the process is exiting; each write attempt runs under
// context.WithoutCancel, so a healthy write at SIGTERM completes. The default is context.Background().
func (p *Pipeline) WithContext(ctx context.Context) *Pipeline {
	p.ctx = ctx
	return p
}

// WithActivity makes the pipeline publish what it stores to the live feed (never message text; Publish never blocks).
func (p *Pipeline) WithActivity(b *activity.Bus) *Pipeline {
	p.bus = b
	return p
}

// publish tells the feed about a stored or skipped result: kind, chat and a type or count, never text.
func (p *Pipeline) publish(r Result, stored bool) {
	e := activity.Event{At: p.now(), Chat: r.Chat.JID}
	switch {
	case !stored:
		e.Kind, e.Detail = "excluded_skip", string(r.Kind)
	case r.Kind == KindUndecryptable:
		e.Kind, e.Detail = "gap", "undecryptable"
	case r.Message != nil:
		e.Kind, e.Detail = "message", "text"
		if r.Media != nil {
			e.Detail = r.Message.Kind
		}
	case r.Event != nil:
		e.Kind, e.Detail = "event", r.Event.Kind
	case r.ChatEvent != nil:
		e.Kind, e.Detail = "chat_event", r.ChatEvent.Kind
	default:
		return
	}
	p.bus.Publish(e)
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
// policy gives up, then keeps retrying at the longest interval until the write succeeds or the retrying is stopped.
// Retrying stops when ctx or the pipeline's serve context ends (a back-off sleep, at most writeMaxWait, is not
// interrupted by the latter); that context never cancels a write attempt itself (fn runs under the caller's context),
// so a healthy write at SIGTERM completes and only a failing one is abandoned.
func (p *Pipeline) persist(ctx context.Context, what string, fn func() error) error {
	stopped := func() bool { return ctx.Err() != nil || p.ctx.Err() != nil }
	policy := retry.Policy{Max: writeAttempts, Base: writeBase, Sleep: func(d time.Duration) { p.sleep(ctx, d) }, Permanent: isPermanent}
	_, err := retry.Do(ctx, policy, fn)
	if err == nil {
		return nil
	}
	p.health.WriteFailed(err)
	if isPermanent(err) {
		return p.rejected(what, err)
	}
	p.log.Error("write failing, retrying until it succeeds", "what", what, "err", err) // the alert carries no detail
	for !stopped() {
		p.sleep(ctx, writeMaxWait)
		if err = fn(); err == nil {
			return nil
		}
		if isPermanent(err) {
			return p.rejected(what, err)
		}
	}
	return fmt.Errorf("%s: %w: %w", what, errStopped, err)
}

// errStopped marks a write that persist abandoned because the serve context ended before the write succeeded.
var errStopped = errors.New("stopped before the write succeeded")

// keep evaluates the exclusion filter under persist: a failed alias lookup is retried (and alerted once through
// WriteFailed) until it answers, so an excluded chat is never stored and a kept one is never dropped.
func (p *Pipeline) keep(ctx context.Context, chat string, kind Kind) (keep bool, err error) {
	keep, _, err = p.decide(ctx, chat, kind)
	return keep, err
}

// decide is keep that also reports whether a refusal is an exclusion rule (as opposed to a never-archived event).
func (p *Pipeline) decide(ctx context.Context, chat string, kind Kind) (keep, excluded bool, err error) {
	err = p.persist(ctx, "check exclusion of "+chat, func() (e error) {
		keep, excluded, e = p.filter.Decide(chat, kind)
		return e
	})
	return keep && err == nil, excluded && err == nil, err
}

// rejected logs and returns a write that can never succeed.
func (p *Pipeline) rejected(what string, err error) error {
	err = fmt.Errorf("%s: %w", what, err)
	p.log.Error("write rejected, not retrying", "what", what, "err", err)
	return err
}

// Handle processes one whatsmeow event and reports whether whatsmeow may acknowledge it. It returns false when the
// serve context ended before the event was stored, so whatsmeow leaves the event unacknowledged. A stored event and a
// permanent rejection (logged and alerted by persist) both return true, except for a history chunk: any import error
// returns false, because its ledger row is written only once the whole chunk is stored.
func (p *Pipeline) Handle(evt any) (ack bool) {
	ack = true
	guard.Run(p.log, "handler", func(string, any) { p.health.Panic("handler") }, func() {
		ctx := context.WithoutCancel(p.ctx) // writes are not cancelled by SIGTERM; persist stops retrying on p.ctx
		now := p.now()
		logErr := func(err error) {
			if err != nil {
				p.log.Error("event not stored", "type", fmt.Sprintf("%T", evt), "err", err)
				if errors.Is(err, errStopped) {
					ack = false
				}
			}
		}
		switch e := evt.(type) {
		case *events.Message:
			r := FromMessage(e, "live", now)
			p.nameVote(ctx, e, &r)
			logErr(p.Ingest(ctx, r))
		case *events.UndecryptableMessage:
			logErr(p.Ingest(ctx, FromUndecryptable(e, now)))
		case *events.HistorySync:
			_, _, err := p.ImportHistory(ctx, e)
			logErr(err)
			if err != nil {
				ack = false // a chunk that is not fully stored is not acked, so WhatsApp delivers it again
			}
		case *events.GroupInfo, *events.JoinedGroup, *events.Archive, *events.Mute, *events.Pin:
			if r, ok := FromChatEvent(e, now); ok {
				logErr(p.Ingest(ctx, r))
			}
		case *events.MediaRetry:
			p.queue.OnRetry(ctx, e)
		case *events.Connected:
			p.health.SetState("connected", "")
			p.queue.Wake()
		case *events.KeepAliveTimeout:
			p.health.SetState("degraded", fmt.Sprintf("keepalive timeout (failures=%d)", e.ErrorCount))
			p.queue.Wake()
		case *events.KeepAliveRestored:
			p.health.SetState("connected", "")
			p.queue.Wake()
		case *events.OfflineSyncPreview:
			if sink, ok := p.health.(interface{ OfflineSyncPreview(int) }); ok {
				sink.OfflineSyncPreview(e.Messages)
			}
		case *events.OfflineSyncCompleted:
			if sink, ok := p.health.(interface{ OfflineSyncCompleted(int) }); ok {
				sink.OfflineSyncCompleted(e.Count)
			}
		case *events.Disconnected:
			p.health.SetState("disconnected", "")
			p.queue.Wake()
		case *events.LoggedOut:
			p.health.SetState("logged_out", fmt.Sprint(e.Reason))
			p.queue.Wake()
		case *events.ClientOutdated:
			p.health.SetState("client_outdated", "")
			p.queue.Wake()
		case *events.TemporaryBan:
			p.health.SetState("temp_banned", fmt.Sprint(e.Expire))
			p.queue.Wake()
		case *events.StreamReplaced:
			p.health.SetState("stream_replaced", "")
			p.queue.Wake()
		case *events.ConnectFailure:
			p.health.SetState("disconnected", fmt.Sprintf("%v %s", e.Reason, e.Message))
			p.queue.Wake()
		default:
			p.log.Debug("ignored event", "type", fmt.Sprintf("%T", evt))
		}
	})
	return ack
}

func (p *Pipeline) Ingest(ctx context.Context, r Result) error { return p.ingest(ctx, r, true) }

// ingest is the one write path; quiet history messages are not published one by one (the chunk is, once).
func (p *Pipeline) ingest(ctx context.Context, r Result, publish bool) error {
	for _, m := range r.Mappings {
		if err := p.persist(ctx, "insert id mapping", func() error {
			_, err := p.writer.InsertIDMapping(ctx, m)
			return err
		}); err != nil {
			return err
		}
	}
	if r.Chat.ParentJID != "" {
		p.filter.DiscoverCommunityChild(r.Chat.JID, r.Chat.ParentJID)
	}
	if keep, excluded, err := p.decide(ctx, r.Chat.JID, r.Kind); err != nil || !keep {
		if err == nil && excluded && publish { // status posts and never-stored kinds are refused silently
			p.publish(r, false)
		}
		return err
	}
	if r.Media != nil { // the rules are decided before the write, so the media row is stored with its decision
		if err := p.persist(ctx, fmt.Sprintf("decide media in %s", r.Chat.JID), func() error { return p.queue.Decide(ctx, r.Media) }); err != nil {
			return err
		}
	}
	batch := batchOf(r)
	if err := p.persist(ctx, fmt.Sprintf("write %s in %s", r.Kind, r.Chat.JID), func() error {
		_, err := p.writer.Apply(ctx, batch)
		return err
	}); err != nil {
		return err
	}
	if r.Media != nil {
		p.queue.Wake()
	}
	p.health.Seen(r.Received, r.FromMe)
	if p.seen != nil && r.Chat.JID != "" {
		// A UI flag, written after capture is done: a failure is logged and the chat is tried again at its next
		// sighting. It never fails, retries or delays the event.
		if err := p.seen.mark(ctx, r.Chat.JID, r.Received.Unix()); err != nil {
			p.log.Warn("chat not marked as seen; will retry at its next message", "chat", r.Chat.JID, "err", err)
		}
	}
	if publish {
		p.publish(r, true)
	}
	return nil
}

// batchOf is the one transaction an event is written in: its chat and sender directory rows, the message, event or chat
// event, and the media row of a message. The sender's name is stamped with the time the event was received.
func batchOf(r Result) store.Batch {
	b := store.Batch{Message: r.Message, Event: r.Event, ChatEvent: r.ChatEvent, Media: r.Media}
	if r.Chat.JID != "" {
		b.Chat = &r.Chat
	}
	if r.Sender.JID != "" {
		sender := r.Sender
		sender.UpdatedAt = r.Received.Unix()
		b.Sender = &sender
	}
	return b
}
