package wa

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/logsafe"
)

// historyDownloader fetches and decodes the history chunk a notification points to.
type historyDownloader func(ctx context.Context, n *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error)

// historyDownloadTimeout bounds one history download. The handler runs synchronously, so a hung download would
// stall every live event; a download cut by it is not acked and comes back on redelivery.
const historyDownloadTimeout = 2 * time.Minute

// historyMaxBlobBytes bounds both the downloaded compressed blob and its decompressed protobuf. Real chunks are tens
// of MB; 256 MiB leaves generous headroom without allowing a corrupt response or decompression bomb to exhaust RAM.
const historyMaxBlobBytes int64 = 256 << 20

// errHistoryGone marks a history blob the server no longer has (HTTP 404 or 410). It can never be downloaded, so
// its notification is logged and acked (D4).
var errHistoryGone = errors.New("history blob gone from the server")

// historyErr wraps whatsmeow's typed 404 and 410 download errors in errHistoryGone and leaves every other error as is.
func historyErr(err error) error {
	if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) || errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
		return fmt.Errorf("%w: %w", errHistoryGone, err)
	}
	return err
}

// liveHistoryDownloader downloads through whatsmeow. synchronousStorage=false keeps whatsmeow's own side storage
// (push names, message secrets, LID mappings in session.db) as its automatic download does it.
func liveHistoryDownloader(cli *whatsmeow.Client) historyDownloader {
	return func(ctx context.Context, n *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
		hs, err := downloadHistoryBounded(ctx, cli, n)
		return hs, historyErr(err)
	}
}

var errHistoryTooLarge = errors.New("history blob exceeds the size limit")

func downloadHistoryBounded(ctx context.Context, cli *whatsmeow.Client, n *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error) {
	if err := checkHistoryDeclaredSize(n); err != nil {
		return nil, err
	}
	compressed, err := boundedHistoryBlob(ctx, cli, n)
	if err != nil {
		return nil, err
	}
	if err = checkHistoryDecompressedSize(compressed); err != nil {
		return nil, err
	}
	// DownloadHistorySync performs whatsmeow's session-side storage (message secrets, LID mappings, NCT salt). Giving
	// it the already downloaded and size-checked bytes avoids a second request; its io.ReadAll is now bounded input.
	inline := proto.Clone(n).(*waE2E.HistorySyncNotification)
	inline.InitialHistBootstrapInlinePayload = compressed
	return cli.DownloadHistorySync(ctx, inline, false)
}

func checkHistoryDeclaredSize(n *waE2E.HistorySyncNotification) error {
	if n != nil && n.GetFileLength() > uint64(historyMaxBlobBytes) {
		return fmt.Errorf("%w: declared %d bytes (max %d)", errHistoryTooLarge, n.GetFileLength(), historyMaxBlobBytes)
	}
	return nil
}

func boundedHistoryBlob(ctx context.Context, cli *whatsmeow.Client, n *waE2E.HistorySyncNotification) ([]byte, error) {
	if n == nil {
		return nil, errors.New("missing history notification")
	}
	if n.InitialHistBootstrapInlinePayload != nil {
		if int64(len(n.InitialHistBootstrapInlinePayload)) > historyMaxBlobBytes {
			return nil, fmt.Errorf("%w: inline payload is over %d bytes", errHistoryTooLarge, historyMaxBlobBytes)
		}
		return bytes.Clone(n.InitialHistBootstrapInlinePayload), nil
	}
	file, err := os.CreateTemp("", "whatsapp-history-*")
	if err != nil {
		return nil, fmt.Errorf("create bounded history file: %w", err)
	}
	path := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(path)
	}()
	limited := &boundedHistoryFile{File: file, max: historyMaxBlobBytes}
	if err = cli.DownloadToFile(ctx, n, limited); err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek history download: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, historyMaxBlobBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read history download: %w", err)
	}
	if int64(len(data)) > historyMaxBlobBytes {
		return nil, fmt.Errorf("%w: downloaded payload is over %d bytes", errHistoryTooLarge, historyMaxBlobBytes)
	}
	return data, nil
}

type boundedHistoryFile struct {
	*os.File
	max int64
}

func (f *boundedHistoryFile) Write(p []byte) (int, error) {
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	return f.writeBounded(p, offset, f.File.Write)
}

func (f *boundedHistoryFile) WriteAt(p []byte, offset int64) (int, error) {
	return f.writeBounded(p, offset, func(part []byte) (int, error) { return f.File.WriteAt(part, offset) })
}

func (f *boundedHistoryFile) Truncate(size int64) error {
	if size > f.max {
		return fmt.Errorf("%w: write exceeds %d bytes", errHistoryTooLarge, f.max)
	}
	return f.File.Truncate(size)
}

func (f *boundedHistoryFile) writeBounded(p []byte, offset int64, write func([]byte) (int, error)) (int, error) {
	remaining := f.max - offset
	if remaining <= 0 {
		return 0, fmt.Errorf("%w: write exceeds %d bytes", errHistoryTooLarge, f.max)
	}
	if int64(len(p)) <= remaining {
		return write(p)
	}
	n, err := write(p[:remaining])
	if err != nil {
		return n, err
	}
	return n, fmt.Errorf("%w: write exceeds %d bytes", errHistoryTooLarge, f.max)
}

func checkHistoryDecompressedSize(compressed []byte) error {
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("failed to prepare to decompress: %w", err)
	}
	defer func() { _ = zr.Close() }()
	raw, err := io.ReadAll(io.LimitReader(zr, historyMaxBlobBytes+1))
	if err != nil {
		return fmt.Errorf("failed to decompress: %w", err)
	}
	if int64(len(raw)) > historyMaxBlobBytes {
		return fmt.Errorf("%w: decompressed payload is over %d bytes", errHistoryTooLarge, historyMaxBlobBytes)
	}
	return nil
}

// historyHandler wraps h so a history notification from our own phone is acked only once its chunk is durably spooled.
// whatsmeow (ManualHistorySyncDownload) delivers the notification as an *events.Message; it is downloaded under
// historyTimeout. WithHistorySpool moves the chunk to the importer goroutine, while h still gets the marker message
// (archived as a protocol event). The legacy no-spool path remains for replay/unit callers. A download or spool failure
// is not acked; a blob that is gone is logged at Error and acked. Every other event goes to h unchanged.
func (c *Client) historyHandler(ctx context.Context, h func(any) bool) func(any) bool {
	return func(evt any) bool {
		msg, ok := evt.(*events.Message)
		if !ok || !msg.Info.IsFromMe { // whatsmeow too handles history only from our own devices
			return h(evt)
		}
		n := msg.Message.GetProtocolMessage().GetHistorySyncNotification()
		if n == nil {
			return h(evt)
		}
		if err := checkHistoryDeclaredSize(n); err != nil {
			c.log.Error("history chunk is over the size limit; not acked", "sync_type", n.GetSyncType().String(), "chunk_order", n.GetChunkOrder(), "err", err)
			return false
		}
		dctx, cancel := context.WithTimeout(ctx, c.historyTimeout)
		defer cancel()
		hs, err := c.download(dctx, n)
		switch {
		case errors.Is(err, errHistoryGone):
			c.log.Error("history chunk gone from the server; acked without importing it", "sync_type", n.GetSyncType().String(), "chunk_order", n.GetChunkOrder(), "err", logsafe.RedactURLs(err.Error()))
			return h(evt)
		case err != nil:
			level := slog.LevelError
			if errors.Is(err, context.Canceled) && ctx.Err() != nil { // shutdown cut the download: expected
				level = slog.LevelInfo
			}
			c.log.Log(context.WithoutCancel(ctx), level, "history download failed; not acked, so WhatsApp delivers it again", "sync_type", n.GetSyncType().String(), "chunk_order", n.GetChunkOrder(), "err", logsafe.RedactURLs(err.Error()))
			return false
		}
		if c.historySpool != nil {
			if err = c.historySpool(context.WithoutCancel(ctx), n, hs); err != nil {
				c.log.Error("history spool write failed; not acked, so WhatsApp delivers it again", "sync_type", n.GetSyncType().String(), "chunk_order", n.GetChunkOrder(), "err", logsafe.RedactURLs(err.Error()))
				return false
			}
			return h(evt)
		}
		return h(evt) && h(&events.HistorySync{Data: hs, Notification: n})
	}
}
