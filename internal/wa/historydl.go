package wa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-archiver/internal/logsafe"
)

// historyDownloader fetches and decodes the history chunk a notification points to.
type historyDownloader func(ctx context.Context, n *waE2E.HistorySyncNotification) (*waHistorySync.HistorySync, error)

// historyDownloadTimeout bounds one history download. The handler runs synchronously, so a hung download would
// stall every live event; a download cut by it is not acked and comes back on redelivery.
const historyDownloadTimeout = 2 * time.Minute

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
		hs, err := cli.DownloadHistorySync(ctx, n, false)
		return hs, historyErr(err)
	}
}

// historyHandler wraps h so a history notification from our own phone is acked only once its chunk is stored.
// whatsmeow (ManualHistorySyncDownload) delivers the notification as an *events.Message; it is downloaded under
// historyTimeout, then h gets the message itself (archived as a protocol event, as before) and the chunk as an
// *events.HistorySync. The verdict is true only when h stored both. A download that fails or times out is not acked;
// a blob that is gone is logged at Error and acked. Every other event goes to h unchanged.
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
		return h(evt) && h(&events.HistorySync{Data: hs, Notification: n})
	}
}
