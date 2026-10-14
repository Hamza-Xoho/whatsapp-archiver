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
	_ = q.store.ResetMediaOnStart(ctx, q.clock.Now())
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
				guard.Run(q.log, "media worker", onPanic, func() { q.work(ctx, &current) })
			}
		}()
	}
	<-ctx.Done()
	q.wg.Wait()
}
func (q *Queue) work(ctx context.Context, current **store.Media) {
	for {
		job, err := q.store.ClaimMedia(ctx)
		if err != nil || job == nil {
			return
		}
		*current = job
		q.download(ctx, job)
		*current = nil
	}
}

// save writes the row; a failed write is logged and the row is re-claimable after a restart.
func (q *Queue) save(ctx context.Context, m store.Media) {
	if err := q.store.UpdateMedia(ctx, m); err != nil {
		q.log.Error("media row not saved", "chat", m.ChatJID, "id", m.MessageID, "state", m.State, "err", err)
	}
}
func (q *Queue) download(ctx context.Context, m *store.Media) {
	var data []byte
	attempts, err := retry.Do(ctx, retry.Policy{Max: 5, Base: 10 * time.Millisecond}, func() error { var e error; data, e = q.down.Download(ctx, m.RawProto, m.RetryDirectPath); return e })
	m.Attempts += attempts
	m.UpdatedAt = q.clock.Now().Unix()
	if errors.Is(err, wa.ErrMediaExpired) {
		m.State = "expired"
		m.Reason = err.Error()
		if q.limiter.Allow() {
			if e := q.down.RequestMediaRetry(ctx, wa.MediaRef{}, m.RawProto); e == nil {
				m.State = "retry_requested"
			}
		}
		q.save(ctx, *m)
		return
	}
	if err != nil {
		m.State = "failed"
		m.Reason = err.Error()
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
