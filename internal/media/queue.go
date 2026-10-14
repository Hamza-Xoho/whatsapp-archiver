package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go.mau.fi/whatsmeow/types/events"
	"golang.org/x/time/rate"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/guard"
	"whatsapp-archiver/internal/retry"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

// A download round is roundAttempts tries 1+2+4+8 s apart; a row that still fails is tried again requeueAfter later,
// until maxAttempts tries in all.
const (
	roundAttempts = 5
	maxAttempts   = 20
	requeueAfter  = 15 * time.Minute
)

type Downloader interface {
	Download(context.Context, []byte, string) ([]byte, error)
	RequestMediaRetry(context.Context, wa.MediaRef, []byte) error
	ApplyMediaRetry(*events.MediaRetry, []byte) (string, error)
}
type Clock interface{ Now() time.Time }
type Queue struct {
	dir     string
	rules   config.MediaRules
	store   *store.Store
	down    Downloader
	clock   Clock
	log     *slog.Logger
	wake    chan struct{}
	limiter *rate.Limiter
	onPanic func(string)
	sleep   func(time.Duration) // backoff wait; nil waits on a timer that stops with the context
	pass    sync.Mutex          // one request pass at a time
	wg      sync.WaitGroup
}

// NewQueue builds the queue; onPanic (may be nil) is told "media" for every recovered worker panic.
func NewQueue(dir string, r config.MediaRules, st *store.Store, d Downloader, c Clock, l *slog.Logger, onPanic func(string)) *Queue {
	workers := r.Workers
	if workers < 1 {
		workers = 1
	}
	r.Workers = workers
	rpm := r.RetryRequestsPerMinute
	if rpm < 1 {
		rpm = 10
	}
	return &Queue{dir: dir, rules: r, store: st, down: d, clock: c, log: l, wake: make(chan struct{}, 1), limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm), onPanic: onPanic}
}
func (q *Queue) Enqueue(ctx context.Context, m store.Media) error {
	m.State, m.Reason = decide(m, q.rules)
	m.UpdatedAt = q.clock.Now().Unix()
	if err := q.store.InsertMedia(ctx, m); err != nil {
		return err
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}
func (q *Queue) Run(ctx context.Context) {
	if err := q.store.ResetMediaOnStart(ctx, q.clock.Now()); err != nil {
		q.log.Error("stuck media rows not reset at start", "err", err)
	}
	for i := 0; i < q.rules.Workers; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			var current *store.Media // the row this worker is on; a panic marks it failed
			onPanic := func(string, any) {
				if current != nil {
					current.State, current.Reason, current.UpdatedAt = "failed", "panic", q.clock.Now().Unix()
					q.save(ctx, *current)
					current = nil
				}
				if q.onPanic != nil {
					q.onPanic("media")
				}
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-q.wake:
				case <-time.After(100 * time.Millisecond):
				}
				guard.Run(q.log, "media worker", onPanic, func() {
					q.work(ctx, &current)
					q.requestPass(ctx, &current)
				})
			}
		}()
	}
	<-ctx.Done()
	q.wg.Wait()
}
func (q *Queue) work(ctx context.Context, current **store.Media) {
	for {
		job, err := q.store.ClaimMedia(ctx, q.clock.Now())
		if err != nil || job == nil {
			return
		}
		*current = job
		q.download(ctx, job)
		*current = nil
	}
}

// requestPass fails re-upload requests unanswered for 24 hours, then asks for re-uploads of expired rows while the
// limiter (driven by the queue's clock) allows; the rest stay expired for a later pass, also after a restart.
func (q *Queue) requestPass(ctx context.Context, current **store.Media) {
	if !q.pass.TryLock() {
		return
	}
	defer q.pass.Unlock()
	now := q.clock.Now()
	if err := q.store.FailStaleRetries(ctx, now); err != nil {
		q.log.Error("stale re-upload requests not failed", "err", err)
	}
	for ctx.Err() == nil {
		m, err := q.store.NextExpiredMedia(ctx)
		if err != nil {
			q.log.Error("expired media not read", "err", err)
			return
		}
		if m == nil {
			return
		}
		if reason := noMoreRequests(m); reason != "" {
			m.State, m.Reason, m.UpdatedAt = "failed", reason, now.Unix()
			if !q.save(ctx, *m) {
				return // the row is still expired; the next pass tries again
			}
			continue
		}
		if !q.limiter.AllowN(now, 1) {
			return
		}
		*current = m
		sent := q.request(ctx, m, now)
		*current = nil
		if !sent {
			return // likely not connected; the next pass tries again
		}
	}
}

// noMoreRequests says why m must not be asked for a (further) re-upload, or "" when it may: a re-upload was
// already applied and expired again, or its attempts are used up.
func noMoreRequests(m *store.Media) string {
	switch {
	case m.RetryDirectPath != "":
		return "re-uploaded media expired"
	case m.Attempts >= maxAttempts:
		return "download attempts exhausted"
	}
	return ""
}

// request asks for a re-upload of m; it reports false when the request failed and m is still expired.
func (q *Queue) request(ctx context.Context, m *store.Media, now time.Time) bool {
	ref := wa.MediaRef{Chat: m.ChatJID, Sender: m.SenderJID, ID: m.MessageID, FromMe: m.FromMe}
	switch err := q.down.RequestMediaRetry(ctx, ref, m.RawProto); {
	case err == nil:
		m.State = "retry_requested"
	case errors.Is(err, wa.ErrNoMediaKey):
		m.State, m.Reason = "failed", wa.ErrNoMediaKey.Error()
	default:
		q.log.Warn("media re-upload request failed", "chat", m.ChatJID, "id", m.MessageID, "err", err)
		return false
	}
	m.UpdatedAt = now.Unix()
	q.save(ctx, *m)
	return true
}

// save writes the row; a failed write is logged, reported as false, and the row is re-claimable after a restart.
func (q *Queue) save(ctx context.Context, m store.Media) bool {
	if err := q.store.UpdateMedia(ctx, m); err != nil {
		q.log.Error("media row not saved", "chat", m.ChatJID, "id", m.MessageID, "state", m.State, "err", err)
		return false
	}
	return true
}
func (q *Queue) download(ctx context.Context, m *store.Media) {
	var data []byte
	policy := retry.Policy{Max: roundAttempts, Base: time.Second, Sleep: q.sleep, Permanent: func(e error) bool { return errors.Is(e, wa.ErrMediaExpired) }}
	attempts, err := retry.Do(ctx, policy, func() error { var e error; data, e = q.down.Download(ctx, m.RawProto, m.RetryDirectPath); return e })
	m.Attempts += attempts
	now := q.clock.Now()
	m.UpdatedAt = now.Unix()
	if errors.Is(err, wa.ErrMediaExpired) {
		m.State = "expired" // the next request pass asks for a re-upload
		m.Reason = err.Error()
		q.save(ctx, *m)
		return
	}
	if err != nil {
		m.State = "failed"
		m.Reason = err.Error()
		if m.Attempts < maxAttempts {
			m.State = "pending" // a later round, after the network had time to come back
			m.NextAttemptAt = now.Add(requeueAfter).Unix()
		}
		q.save(ctx, *m)
		return
	}
	path := q.path(*m)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		tmp := path + ".tmp"
		err = os.WriteFile(tmp, data, 0o600)
		if err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		m.State = "failed"
		m.Reason = err.Error()
	} else {
		sum := sha256.Sum256(data)
		m.State = "done"
		m.Path = path
		m.SHA256 = hex.EncodeToString(sum[:])
		m.Reason = ""
	}
	q.save(ctx, *m)
}
func (q *Queue) OnRetry(ctx context.Context, e *events.MediaRetry) {
	m, err := q.store.MediaByID(ctx, e.ChatID.String(), string(e.MessageID))
	if err != nil {
		return
	}
	path, err := q.down.ApplyMediaRetry(e, m.RawProto)
	if err != nil {
		q.log.Warn("media retry answer not applied", "chat", m.ChatJID, "id", m.MessageID, "err", err)
		return
	}
	m.RetryDirectPath = path
	m.State = "pending"
	m.UpdatedAt = q.clock.Now().Unix()
	q.save(ctx, m)
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

var unsafe = regexp.MustCompile(`[<>:"/\\|?*]+`)

func (q *Queue) path(m store.Media) string {
	ext := ""
	if values, _ := mime.ExtensionsByType(m.MIME); len(values) > 0 {
		ext = values[0]
	}
	month := time.Unix(m.MessageTimestamp, 0).UTC().Format("2006-01")
	return filepath.Join(q.dir, "media", unsafe.ReplaceAllString(m.ChatJID, "_"), month, fmt.Sprintf("%s%s", unsafe.ReplaceAllString(m.MessageID, "_"), ext))
}
