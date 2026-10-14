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
	wg      sync.WaitGroup
}

func NewQueue(dir string, r config.MediaRules, st *store.Store, d Downloader, c Clock, l *slog.Logger) *Queue {
	workers := r.Workers
	if workers < 1 {
		workers = 1
	}
	r.Workers = workers
	rpm := r.RetryRequestsPerMinute
	if rpm < 1 {
		rpm = 10
	}
	return &Queue{dir: dir, rules: r, store: st, down: d, clock: c, log: l, wake: make(chan struct{}, 1), limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm)}
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
			for {
				select {
				case <-ctx.Done():
					return
				case <-q.wake:
				case <-time.After(100 * time.Millisecond):
				}
				guard.Run(q.log, "media worker", nil, func() { q.work(ctx) })
			}
		}()
	}
	<-ctx.Done()
	q.wg.Wait()
}
func (q *Queue) work(ctx context.Context) {
	for {
		job, err := q.store.ClaimMedia(ctx)
		if err != nil || job == nil {
			return
		}
		q.download(ctx, job)
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
		_ = q.store.UpdateMedia(ctx, *m)
		return
	}
	if err != nil {
		m.State = "failed"
		m.Reason = err.Error()
		_ = q.store.UpdateMedia(ctx, *m)
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
	_ = q.store.UpdateMedia(ctx, *m)
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
	_ = q.store.UpdateMedia(ctx, m)
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
