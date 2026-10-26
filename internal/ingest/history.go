package ingest

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/store"
)

type historyStore interface {
	InsertHistoryChunk(context.Context, store.HistoryChunk) error
}

// HistoryParser turns one history message into the event the live path would deliver; internal/wa provides it.
type HistoryParser func(chat types.JID, web *waWeb.WebMessageInfo) (*events.Message, error)

// WithHistoryParser sets the parser ImportHistory uses for every message.
func (p *Pipeline) WithHistoryParser(parse HistoryParser) *Pipeline {
	p.parse = parse
	return p
}

func (p *Pipeline) ImportHistory(ctx context.Context, evt *events.HistorySync) (stored, skipped int, err error) {
	if evt == nil || evt.Data == nil {
		return 0, 0, nil
	}
	hs := evt.Data
	now := p.now()
	ledger, ok := p.writer.(historyStore)
	if !ok {
		return 0, 0, fmt.Errorf("writer does not support history ledger")
	}
	if p.parse == nil {
		return 0, 0, fmt.Errorf("pipeline has no history parser")
	}
	// The chunk's content is its identity: distinct chunks never collide and a redelivered chunk is ledgered once.
	raw, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(hs)
	if err != nil {
		return 0, 0, fmt.Errorf("encode history chunk: %w", err)
	}
	chunk := store.HistoryChunk{ID: stableID(string(raw)), Stage: store.StageImported, SyncType: hs.GetSyncType().String(), ChunkOrder: int(hs.GetChunkOrder()), Progress: int(hs.GetProgress()), ConversationCount: len(hs.GetConversations()), ReceivedAt: now.Unix()}
	for _, conv := range hs.GetConversations() {
		if conv == nil {
			continue
		}
		chunk.MessageCount += len(conv.GetMessages())
		for _, item := range conv.GetMessages() {
			if item == nil || item.GetMessage() == nil {
				skipped++
				continue
			}
			ts := int64(item.GetMessage().GetMessageTimestamp())
			if chunk.Oldest == 0 || ts < chunk.Oldest {
				chunk.Oldest = ts
			}
			if ts > chunk.Newest {
				chunk.Newest = ts
			}
		}
	}
	for _, mapping := range hs.GetPhoneNumberToLidMappings() {
		if mapping == nil {
			continue
		}
		m := store.IDMapping{LID: mapping.GetLidJID(), PN: mapping.GetPnJID(), Source: "history", FirstSeen: now}
		if err = p.persist(ctx, "insert history id mapping", func() error {
			_, mapErr := p.writer.InsertIDMapping(ctx, m)
			return mapErr
		}); err != nil {
			return
		}
	}
	for _, conv := range hs.GetConversations() {
		if conv == nil {
			continue
		}
		chat := conv.GetID()
		keepChat := false
		if chat != "" {
			if keepChat, err = p.keep(ctx, chat, KindMessage); err != nil {
				return stored, skipped, err
			}
		}
		if keepChat {
			if upErr := p.persist(ctx, "upsert history chat", func() error {
				_, upErr := p.writer.Apply(ctx, store.Batch{Chat: &store.Chat{JID: chat, Name: conv.GetName()}})
				return upErr
			}); upErr != nil {
				return stored, skipped, upErr
			}
		}
		chatJID, _ := types.ParseJID(chat) // an unparsable ID leaves it empty, so the parser uses the key's chat
		for _, item := range conv.GetMessages() {
			if item == nil || item.GetMessage() == nil {
				continue
			}
			web := item.GetMessage()
			message, parseErr := p.parse(chatJID, web)
			if parseErr != nil {
				p.log.Warn("history message not parsed", "chat", chat, "id", web.GetKey().GetID(), "err", parseErr)
				skipped++
				continue
			}
			var r Result
			if web.GetMessage() == nil {
				// A stub (revoke, ciphertext, group change marker …) has no body: it is a stub event, never a message row.
				r = FromStub(message, web, now)
				p.log.Info("history stub archived", "chat", chat, "id", web.GetKey().GetID(), "stub", web.GetMessageStubType().String())
			} else {
				r = FromMessage(message, "history", now)
			}
			keepMessage, keepErr := p.keep(ctx, r.Chat.JID, r.Kind)
			if keepErr != nil {
				return stored, skipped, keepErr
			}
			if !keepMessage {
				skipped++
				continue
			}
			if ingestErr := p.ingest(ctx, r, false); ingestErr != nil {
				if !isPermanent(ingestErr) {
					return stored, skipped, ingestErr // only a stopped context ends the chunk early
				}
				skipped++ // persist has logged the rejected write at Error and reported it once
				continue
			}
			stored++
		}
	}
	// The ledger row claims the whole chunk, so it is written last: a chunk cut short (SIGTERM, a write that kept
	// failing) has no row and is imported again when WhatsApp redelivers it.
	if err = p.persist(ctx, "insert history chunk", func() error { return ledger.InsertHistoryChunk(ctx, chunk) }); err != nil {
		return stored, skipped, err
	}
	p.bus.Publish(activity.Event{At: now, Kind: "history_chunk", Detail: fmt.Sprintf("chunk %s #%d · %d messages", chunk.SyncType, chunk.ChunkOrder, chunk.MessageCount)})
	p.log.Info("history chunk imported", "progress", chunk.Progress, "stored", stored, "skipped", skipped)
	return
}
