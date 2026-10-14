package ingest

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"whatsapp-archiver/internal/store"
)

type historyStore interface {
	InsertHistoryChunk(context.Context, store.HistoryChunk) error
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
	chunk := store.HistoryChunk{ID: stableID(fmt.Sprint(hs.GetSyncType()), fmt.Sprint(hs.GetChunkOrder()), fmt.Sprint(hs.GetProgress()), fmt.Sprint(len(hs.GetConversations()))), SyncType: hs.GetSyncType().String(), ChunkOrder: int(hs.GetChunkOrder()), Progress: int(hs.GetProgress()), ConversationCount: len(hs.GetConversations()), ReceivedAt: now.Unix()}
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
	if err = ledger.InsertHistoryChunk(ctx, chunk); err != nil {
		return
	}
	for _, mapping := range hs.GetPhoneNumberToLidMappings() {
		if mapping == nil {
			continue
		}
		_, err = p.writer.InsertIDMapping(ctx, store.IDMapping{LID: mapping.GetLidJID(), PN: mapping.GetPnJID(), Source: "history", FirstSeen: now})
		if err != nil {
			return
		}
	}
	for _, conv := range hs.GetConversations() {
		if conv == nil {
			continue
		}
		chat := conv.GetID()
		if chat != "" {
			if upErr := p.writer.UpsertChat(ctx, store.Chat{JID: chat, Name: conv.GetName()}); upErr != nil {
				return stored, skipped, upErr
			}
		}
		for _, item := range conv.GetMessages() {
			if item == nil || item.GetMessage() == nil {
				continue
			}
			message, parseErr := historyMessage(chat, item.GetMessage())
			if parseErr != nil {
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

func historyMessage(chat string, web *waWeb.WebMessageInfo) (*events.Message, error) {
	key := web.GetKey()
	if key == nil {
		return nil, fmt.Errorf("missing key")
	}
	chatJID, err := types.ParseJID(chat)
	if err != nil {
		return nil, err
	}
	sender := chatJID
	if key.GetParticipant() != "" {
		sender, err = types.ParseJID(key.GetParticipant())
		if err != nil {
			return nil, err
		}
	}
	info := types.MessageInfo{MessageSource: types.MessageSource{Chat: chatJID, Sender: sender, IsFromMe: key.GetFromMe(), IsGroup: chatJID.Server == types.GroupServer}, ID: types.MessageID(key.GetID()), Timestamp: time.Unix(int64(web.GetMessageTimestamp()), 0)}
	return &events.Message{Info: info, Message: web.GetMessage(), RawMessage: web.GetMessage(), SourceWebMsg: web}, nil
}

var _ = waHistorySync.HistorySync_INITIAL_BOOTSTRAP
