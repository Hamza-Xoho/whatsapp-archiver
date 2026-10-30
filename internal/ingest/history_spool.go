package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

// historySpoolStore is the durable boundary between the synchronous WhatsApp handler and the asynchronous importer.
type historySpoolStore interface {
	SpoolReceive(context.Context, store.Spool, store.HistoryChunk) (int64, bool, error)
	SpoolTodo(context.Context, int) ([]store.Spool, error)
	SpoolImported(context.Context, int64, int64, store.HistoryChunk) error
	SpoolFailed(context.Context, int64, string) error
}

// HistorySpool durably receives downloaded chunks and imports them outside the WhatsApp event handler. Run is owned by
// the service lifecycle. The buffered wake means Receive never waits for the importer after its database commit.
type HistorySpool struct {
	store    historySpoolStore
	pipeline *Pipeline
	log      *slog.Logger
	now      func() time.Time
	wake     chan struct{}
}

// NewHistorySpool creates the history receiver/importer. Nothing starts until Run is called.
func NewHistorySpool(st historySpoolStore, pipeline *Pipeline, log *slog.Logger) *HistorySpool {
	if log == nil {
		log = slog.Default()
	}
	return &HistorySpool{store: st, pipeline: pipeline, log: log, now: time.Now, wake: make(chan struct{}, 1)}
}

// historySpoolMeta is an explicit allowlist of non-key HistorySyncNotification fields. Do not replace it with JSON
// marshaling of the notification: that proto contains the media key, hashes, encrypted handle and inline payload.
type historySpoolMeta struct {
	SyncType                 string `json:"sync_type"`
	ChunkOrder               uint32 `json:"chunk_order"`
	Progress                 uint32 `json:"progress"`
	OriginalMessageID        string `json:"original_message_id"`
	OldestMessageTimestamp   int64  `json:"oldest_msg_timestamp"`
	FileLength               uint64 `json:"file_length"`
	DirectPath               string `json:"direct_path"`
	PeerDataRequestSessionID string `json:"peer_data_request_session_id"`
}

// Receive strips key material, computes identity from the stripped chunk, and atomically stores the spool and its
// received ledger row. Returning nil means the handler may acknowledge the notification. A resend is a successful no-op.
func (h *HistorySpool) Receive(ctx context.Context, n *waE2E.HistorySyncNotification, hs *waHistorySync.HistorySync) error {
	if n == nil || hs == nil {
		return fmt.Errorf("spool history: missing notification or chunk")
	}
	clean := StripHistorySync(hs)
	blob, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(clean)
	if err != nil {
		return fmt.Errorf("spool history: encode stripped chunk: %w", err)
	}
	meta, err := json.Marshal(historySpoolMeta{
		SyncType:                 n.GetSyncType().String(),
		ChunkOrder:               n.GetChunkOrder(),
		Progress:                 n.GetProgress(),
		OriginalMessageID:        n.GetOriginalMessageID(),
		OldestMessageTimestamp:   n.GetOldestMsgInChunkTimestampSec(),
		FileLength:               n.GetFileLength(),
		DirectPath:               n.GetDirectPath(),
		PeerDataRequestSessionID: n.GetPeerDataRequestSessionID(),
	})
	if err != nil {
		return fmt.Errorf("spool history: encode metadata: %w", err)
	}
	receivedAt := h.now().Unix()
	chunkID := stableID(string(blob))
	sp := store.Spool{ChunkID: chunkID, SyncType: clean.GetSyncType().String(), ChunkOrder: int(clean.GetChunkOrder()), Progress: int(clean.GetProgress()), Meta: string(meta), BlobFormat: store.BlobRaw, Blob: blob, ReceivedAt: receivedAt}
	chunk := describeHistory(clean, chunkID, store.StageReceived, receivedAt)
	_, isNew, err := h.store.SpoolReceive(ctx, sp, chunk)
	if err != nil {
		return fmt.Errorf("spool history: %w", err)
	}
	if isNew {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// Run resumes pending rows at startup, processes them oldest first, and stops on context cancellation. A failed row
// remains first in the queue; a bounded retry delay avoids spinning while preserving order.
func (h *HistorySpool) Run(ctx context.Context) {
	for ctx.Err() == nil {
		imported, err := h.ImportNext(ctx)
		if err != nil {
			if ctx.Err() == nil {
				h.log.Error("history spool import failed; will retry", "err", err)
			}
			if !waitHistory(ctx, h.wake, time.Second) {
				return
			}
			continue
		}
		if imported {
			continue
		}
		if !waitHistory(ctx, h.wake, 0) {
			return
		}
	}
}

func waitHistory(ctx context.Context, wake <-chan struct{}, retry time.Duration) bool {
	if retry == 0 {
		select {
		case <-ctx.Done():
			return false
		case <-wake:
			return true
		}
	}
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

// ImportNext imports the oldest pending row. It is public for deterministic lifecycle/crash tests; production uses Run.
// It returns false,nil when there is no pending row.
func (h *HistorySpool) ImportNext(ctx context.Context) (bool, error) {
	rows, err := h.store.SpoolTodo(ctx, 1)
	if err != nil {
		return false, fmt.Errorf("read history spool: %w", err)
	}
	if len(rows) == 0 {
		return false, nil
	}
	sp := rows[0]
	hs, err := decodeSpooledHistory(sp.Blob, sp.BlobFormat)
	stored, skipped := 0, 0
	if err == nil {
		stored, skipped, err = h.pipeline.importHistory(ctx, &events.HistorySync{Data: hs}, false)
	}
	if err != nil {
		if markErr := h.store.SpoolFailed(context.WithoutCancel(ctx), sp.ID, err.Error()); markErr != nil {
			return false, fmt.Errorf("import history spool row %d: %w (record failure: %v)", sp.ID, err, markErr)
		}
		return false, fmt.Errorf("import history spool row %d: %w", sp.ID, err)
	}
	now := h.now()
	at := now.Unix()
	chunk := describeHistory(hs, sp.ChunkID, store.StageImported, at)
	if err = h.store.SpoolImported(ctx, sp.ID, at, chunk); err != nil {
		return false, fmt.Errorf("mark history spool row %d imported: %w", sp.ID, err)
	}
	h.pipeline.historyImported(now, chunk, stored, skipped)
	return true, nil
}

func decodeSpooledHistory(blob []byte, format string) (*waHistorySync.HistorySync, error) {
	var raw []byte
	switch format {
	case store.BlobRaw:
		raw = blob
	case store.BlobZlib:
		var err error
		if raw, err = inflate(blob); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown history spool format %q", format)
	}
	hs := &waHistorySync.HistorySync{}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(raw, hs); err != nil {
		return nil, fmt.Errorf("decode history spool: %w", err)
	}
	return hs, nil
}
