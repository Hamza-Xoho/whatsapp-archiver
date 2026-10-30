package wa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/config"
)

// historyNotification is the message whatsmeow dispatches for a history chunk when ManualHistorySyncDownload is on.
func historyNotification(fromMe bool) *events.Message {
	n := &waE2E.HistorySyncNotification{SyncType: waE2E.HistorySyncType_FULL.Enum(), ChunkOrder: proto.Uint32(3)}
	msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(), HistorySyncNotification: n}}
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: fromMe}, ID: "N1"}, Message: msg, RawMessage: msg}
}

func historyClient(log *slog.Logger, timeout time.Duration, dl historyDownloader) *Client {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Client{log: log, download: dl, historyTimeout: timeout}
}

// Problem 4: whatsmeow's automatic history download acks the notification before the chunk is stored. The manual
// download hands the chunk to our handler so the ack waits for it.
func TestOpenDownloadsHistoryManually(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), History: config.HistoryRules{DaysLimit: 1, SizeMBLimit: 1}}
	c, err := Open(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.client.ManualHistorySyncDownload {
		t.Fatal("whatsmeow downloads history itself and acks the notification before the chunk is stored")
	}
	if c.download == nil || c.historyTimeout != historyDownloadTimeout {
		t.Fatalf("download set=%v timeout=%s, want the live downloader and %s", c.download != nil, c.historyTimeout, historyDownloadTimeout)
	}
}

// The notification is acked only when the handler stored its chunk; the handler sees the chunk as an events.HistorySync.
func TestHistoryNotificationIsNotAckedUntilItsChunkIsStored(t *testing.T) {
	hs := &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_FULL.Enum(), ChunkOrder: proto.Uint32(3)}
	notif := historyNotification(true)
	for _, verdict := range []bool{false, true} {
		var gotNotif *waE2E.HistorySyncNotification
		c := historyClient(nil, time.Minute, func(_ context.Context, n *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
			gotNotif = n
			return hs, nil
		})
		var chunks []*waHistorySync.HistorySync
		markers := 0
		h := func(evt any) bool {
			if e, ok := evt.(*events.HistorySync); ok {
				chunks = append(chunks, e.Data)
				return verdict
			}
			if evt == notif {
				markers++
			}
			return true
		}
		got := eventHandler(c.historyHandler(t.Context(), h), false, io.Discard, make(chan any, 1))(notif)
		if got != verdict {
			t.Fatalf("handler stored=%v, ack=%v", verdict, got)
		}
		if len(chunks) != 1 || chunks[0] != hs {
			t.Fatalf("handler got chunks %v, want the downloaded chunk once", chunks)
		}
		if gotNotif != notif.Message.GetProtocolMessage().GetHistorySyncNotification() {
			t.Fatal("downloader did not get the notification")
		}
		if markers != 1 {
			t.Fatalf("notification message reached the handler %d times, want once (it is archived as a protocol event)", markers)
		}
	}
}

// ADR-37: once a spool receiver is configured, the history chunk no longer enters the synchronous event handler.
// The notification is acknowledged only after Receive durably spooled it and the marker message was archived.
func TestHistoryNotificationIsAckedAfterSpoolAndDoesNotEnterTheLiveHandler(t *testing.T) {
	hs := &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_FULL.Enum(), ChunkOrder: proto.Uint32(3)}
	notif := historyNotification(true)
	order := []string{}
	c := historyClient(nil, time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		order = append(order, "download")
		return hs, nil
	}).WithHistorySpool(func(_ context.Context, n *waE2E.HistorySyncNotification, got *waHistorySync.HistorySync) error {
		order = append(order, "spool")
		if n != notif.Message.GetProtocolMessage().GetHistorySyncNotification() || got != hs {
			t.Fatal("spool receiver did not get the downloaded notification and chunk")
		}
		return nil
	})
	acked := c.historyHandler(t.Context(), func(evt any) bool {
		if _, ok := evt.(*events.HistorySync); ok {
			t.Fatal("history import still runs inside the live event handler")
		}
		order = append(order, "marker")
		return true
	})(notif)
	if !acked || strings.Join(order, ",") != "download,spool,marker" {
		t.Fatalf("acked=%v order=%v, want download, spool, marker, ack", acked, order)
	}

	spoolErr := errors.New("archive write failed")
	c.WithHistorySpool(func(context.Context, *waE2E.HistorySyncNotification, *waHistorySync.HistorySync) error {
		return spoolErr
	})
	called := false
	if c.historyHandler(t.Context(), func(any) bool { called = true; return true })(notif) || called {
		t.Fatal("failed spool was acknowledged or passed to the live handler")
	}
}

// Only our own phone sends history; a notification from anyone else is not downloaded (whatsmeow ignores it too), and
// every other event reaches the handler unchanged.
func TestOnlyOwnHistoryNotificationIsDownloaded(t *testing.T) {
	c := historyClient(nil, time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		t.Fatal("downloaded a history notification that is not from our own phone")
		return nil, nil
	})
	for _, evt := range []any{historyNotification(false), &events.Message{Message: &waE2E.Message{Conversation: proto.String("x")}}, &events.Connected{}} {
		var got []any
		ok := c.historyHandler(t.Context(), func(e any) bool { got = append(got, e); return false })(evt)
		if ok || len(got) != 1 || got[0] != evt {
			t.Fatalf("%T: handler got %v and returned %v, want the event unchanged and its verdict", evt, got, ok)
		}
	}
}

// A download that hangs would stall every live event: it is bounded, and a timeout leaves the notification unacked.
func TestHistoryDownloadIsBoundedAndNotAckedOnTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := historyClient(nil, 20*time.Millisecond, func(ctx context.Context, _ *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		<-ctx.Done()
		return nil, fmt.Errorf("failed to download: %w", ctx.Err())
	})
	called := false
	done := make(chan bool, 1)
	go func() {
		done <- c.historyHandler(ctx, func(any) bool { called = true; return true })(historyNotification(true))
	}()
	select {
	case ok := <-done:
		if ok || called {
			t.Fatalf("timed-out download: ack=%v handler called=%v, want no ack and no call", ok, called)
		}
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("history download is not bounded")
	}
}

// A notification that declares an impossible chunk size is rejected before any allocation or network request. The
// live downloader additionally enforces the same bound while writing, so a dishonest Content-Length cannot bypass it.
func TestOversizeHistoryIsNotDownloadedSpooledOrAcked(t *testing.T) {
	notif := historyNotification(true)
	n := notif.Message.GetProtocolMessage().GetHistorySyncNotification()
	n.FileLength = proto.Uint64(uint64(historyMaxBlobBytes + 1))
	downloaded, spooled, handled := false, false, false
	c := historyClient(nil, time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		downloaded = true
		return &waHistorySync.HistorySync{}, nil
	}).WithHistorySpool(func(context.Context, *waE2E.HistorySyncNotification, *waHistorySync.HistorySync) error {
		spooled = true
		return nil
	})
	if acked := c.historyHandler(t.Context(), func(any) bool { handled = true; return true })(notif); acked || downloaded || spooled || handled {
		t.Fatalf("acked=%v downloaded=%v spooled=%v handled=%v, want bounded rejection before all work", acked, downloaded, spooled, handled)
	}
}

// Any other download error is retried by redelivery: no ack.
func TestHistoryDownloadErrorIsNotAcked(t *testing.T) {
	c := historyClient(nil, time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		return nil, errors.New("failed to download: connection reset")
	})
	if c.historyHandler(t.Context(), func(any) bool { t.Fatal("handler called without a chunk"); return true })(historyNotification(true)) {
		t.Fatal("a failed download was acked")
	}
}

// D4: a blob that is gone (HTTP 404 or 410) can never be downloaded; it is logged at Error and acked.
func TestHistoryGoneIsAckedAndLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := historyClient(log, time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		return nil, fmt.Errorf("download history: %w", errHistoryGone)
	})
	var chunks, markers int
	ok := c.historyHandler(t.Context(), func(evt any) bool {
		if _, isChunk := evt.(*events.HistorySync); isChunk {
			chunks++
		} else {
			markers++
		}
		return true
	})(historyNotification(true))
	if !ok || chunks != 0 || markers != 1 {
		t.Fatalf("gone blob: ack=%v chunks=%d markers=%d, want ack, no chunk, the notification archived once", ok, chunks, markers)
	}
	line := buf.String()
	for _, want := range []string{"level=ERROR", "sync_type=FULL", "chunk_order=3"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log %q lacks %q", line, want)
		}
	}
}

// The live downloader maps whatsmeow's typed 404 and 410 download errors to errHistoryGone, and nothing else.
func TestHistoryGoneWraps404And410(t *testing.T) {
	for _, err := range []error{whatsmeow.ErrMediaDownloadFailedWith404, whatsmeow.ErrMediaDownloadFailedWith410} {
		got := historyErr(fmt.Errorf("failed to download: %w", err))
		if !errors.Is(got, errHistoryGone) || !errors.Is(got, err) {
			t.Fatalf("%v -> %v, want errHistoryGone wrapping it", err, got)
		}
	}
	for _, err := range []error{whatsmeow.ErrMediaDownloadFailedWith403, errors.New("failed to download: connection reset"), context.DeadlineExceeded} {
		if got := historyErr(err); errors.Is(got, errHistoryGone) || !errors.Is(got, err) {
			t.Fatalf("%v -> %v, want it unchanged", err, got)
		}
	}
	if historyErr(nil) != nil {
		t.Fatal("nil error mapped to an error")
	}
}

func TestHistoryDownloadFailureLogIsRedacted(t *testing.T) {
	signed := errors.New("download https://mmg.whatsapp.net/v/t62/h.enc?ccb=11-4&oh=01_SECRET&oe=65F0: status 500")
	for name, err := range map[string]error{"failed": signed, "gone": fmt.Errorf("%w: %w", errHistoryGone, signed)} {
		var buf bytes.Buffer
		c := historyClient(slog.New(slog.NewTextHandler(&buf, nil)), time.Minute, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
			return nil, err
		})
		c.historyHandler(t.Context(), func(any) bool { return true })(historyNotification(true))
		out := buf.String()
		if !strings.Contains(out, "h.enc?REDACTED") || strings.Contains(out, "SECRET") || strings.Contains(out, "ccb=") {
			t.Fatalf("%s: journal line = %q", name, out)
		}
	}
}

// Shutdown cancels the download: that is expected, so it is logged at Info (still not acked, still redacted).
// A timeout with the parent alive stays at Error, and so does a cancellation while the parent is alive.
func TestCancelledHistoryDownloadLogsAtInfo(t *testing.T) {
	signed := "https://mmg.whatsapp.net/v/t62/h.enc?ccb=11-4&oh=01_SECRET&oe=65F0"
	run := func(parentDone bool, timeout time.Duration, err error) (acked bool, out string) {
		var buf bytes.Buffer
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		if parentDone {
			cancel()
		}
		c := historyClient(slog.New(slog.NewTextHandler(&buf, nil)), timeout, func(context.Context, *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
			return nil, err
		})
		acked = c.historyHandler(ctx, func(any) bool { t.Fatal("handler called without a chunk"); return true })(historyNotification(true))
		return acked, buf.String()
	}
	t.Run("shutdown", func(t *testing.T) {
		acked, out := run(true, time.Minute, fmt.Errorf("download %s: %w", signed, context.Canceled))
		if acked || !strings.Contains(out, "level=INFO") || strings.Contains(out, "level=ERROR") || !strings.Contains(out, "history download failed; not acked") ||
			!strings.Contains(out, "sync_type=FULL") || !strings.Contains(out, "chunk_order=3") || strings.Contains(out, "SECRET") || !strings.Contains(out, "h.enc?REDACTED") {
			t.Fatalf("ack=%v line=%q, want no ack and a redacted Info line", acked, out)
		}
	})
	t.Run("timeout with the parent alive stays at Error", func(t *testing.T) {
		acked, out := run(false, time.Minute, fmt.Errorf("failed to download: %w", context.DeadlineExceeded))
		if acked || !strings.Contains(out, "level=ERROR") {
			t.Fatalf("ack=%v line=%q, want no ack and an Error line", acked, out)
		}
	})
	t.Run("cancelled while the parent is alive stays at Error", func(t *testing.T) {
		acked, out := run(false, time.Minute, fmt.Errorf("failed to download: %w", context.Canceled))
		if acked || !strings.Contains(out, "level=ERROR") {
			t.Fatalf("ack=%v line=%q, want no ack and an Error line", acked, out)
		}
	})
	t.Run("another error during shutdown stays at Error", func(t *testing.T) {
		acked, out := run(true, time.Minute, errors.New("failed to download: connection reset"))
		if acked || !strings.Contains(out, "level=ERROR") {
			t.Fatalf("ack=%v line=%q, want no ack and an Error line", acked, out)
		}
	})
}
