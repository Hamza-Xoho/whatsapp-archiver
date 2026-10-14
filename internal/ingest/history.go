package ingest

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
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
	chunk := store.HistoryChunk{ID: stableID(string(raw)), SyncType: hs.GetSyncType().String(), ChunkOrder: int(hs.GetChunkOrder()), Progress: int(hs.GetProgress()), ConversationCount: len(hs.GetConversations()), ReceivedAt: now.Unix()}
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
	if err = p.persist(ctx, "insert history chunk", func() error { return ledger.InsertHistoryChunk(ctx, chunk) }); err != nil {
		return
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
		if chat != "" && p.filter.Keep(chat, KindMessage) {
			if upErr := p.persist(ctx, "upsert history chat", func() error {
				return p.writer.UpsertChat(ctx, store.Chat{JID: chat, Name: conv.GetName()})
			}); upErr != nil {
				return stored, skipped, upErr
			}
		}
		chatJID, _ := types.ParseJID(chat) // an unparsable ID leaves it empty, so the parser uses the key's chat
		for _, item := range conv.GetMessages() {
			if item == nil || item.GetMessage() == nil {
				continue
			}
			message, parseErr := p.parse(chatJID, item.GetMessage())
			if parseErr != nil {
				p.log.Warn("history message not parsed", "chat", chat, "id", item.GetMessage().GetKey().GetID(), "err", parseErr)
				skipped++
				continue
			}
			r := FromMessage(message, "history", now)
			if !p.filter.Keep(r.Chat.JID, r.Kind) {
				skipped++
				continue
			}
			if err = p.Ingest(ctx, r); err != nil {
				return
			}
			stored++
		}
	}
	p.log.Info("history chunk imported", "progress", chunk.Progress, "stored", stored, "skipped", skipped)
	return
}
