package devseed

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

type generator struct {
	opts   Options
	rng    *rand.Rand
	base   int64
	mapped []bool
	media  map[string]bool
}

func newGenerator(opts Options) *generator {
	return &generator{
		opts: opts, rng: rand.New(rand.NewSource(opts.Seed)),
		base:   1761436800 + opts.Seed%86400,
		mapped: make([]bool, opts.Chats), media: map[string]bool{},
	}
}

func (g *generator) message(i int) (ingest.Result, string, []byte) {
	kind := store.MessageKinds[i%len(store.MessageKinds)]
	chatIndex := i
	if i >= g.opts.Chats {
		chatIndex = g.rng.Intn(g.opts.Chats)
	}
	chatIndex %= g.opts.Chats
	lid, phone := chatIDs(chatIndex)
	chat := lid
	if i%2 == 1 {
		chat = phone
	}
	ts := g.base + int64(i)
	id := fmt.Sprintf("SEED-%d-%09d", g.opts.Seed, i)
	text, caption, details := content(kind, i)
	source := []string{"live", "history", "replay"}[i%3]
	msg := &store.Message{
		ChatJID: chat, ID: id, SenderJID: fmt.Sprintf("%d@lid", 800000+chatIndex), PushName: fmt.Sprintf("Sender %03d", chatIndex),
		Kind: kind, Text: text, Caption: caption, Details: details, Source: source,
		Timestamp: ts, ReceivedAt: ts + 1, RawProto: []byte("synthetic:" + id),
		FromMe: i%11 == 0, Forwarded: i%17 == 0, ForwardScore: i % 4,
	}
	result := ingest.Result{
		Kind:    ingest.KindMessage,
		Chat:    store.Chat{JID: chat, Name: fmt.Sprintf("Synthetic chat %03d", chatIndex+1), Type: "direct"},
		Sender:  store.Participant{JID: msg.SenderJID, PushName: msg.PushName},
		Message: msg, FromMe: msg.FromMe, Received: time.Unix(msg.ReceivedAt, 0),
	}
	if !g.mapped[chatIndex] {
		result.Mappings = []store.IDMapping{{LID: lid, PN: phone, Source: "devseed", FirstSeen: time.Unix(ts, 0)}}
		g.mapped[chatIndex] = true
	}
	var file []byte
	if mediaKind(kind) && !g.media[kind] {
		file = []byte(fmt.Sprintf("synthetic %s media for seed %d\n", kind, g.opts.Seed))
		result.Media = &store.Media{
			ChatJID: chat, MessageID: id, SenderJID: msg.SenderJID, Source: source, Type: kind,
			MIME: mediaMIME(kind), Filename: kind + ".txt", Path: kind + ".bin", State: "done",
			MessageTimestamp: ts, EnqueuedAt: ts, UpdatedAt: ts + 1, DoneAt: ts + 1,
		}
		g.media[kind] = true
	}
	return result, kind, file
}

func chatIDs(i int) (string, string) {
	return fmt.Sprintf("%d@lid", 100000+i), fmt.Sprintf("966550%06d@s.whatsapp.net", i)
}

func content(kind string, i int) (text, caption, details string) {
	arabic := fmt.Sprintf("مَرْحَبًا بِكُمْ فِـي الأَرْشِيف %d", i)
	english := fmt.Sprintf("Deterministic archive message %d", i)
	body := english
	if i%2 == 0 {
		body = arabic
	}
	switch kind {
	case "text":
		return body, "", `{}`
	case "image", "audio", "voice", "document", "gif":
		return "", body, `{}`
	case "video":
		return "", body, `{"ptv":true}`
	case "sticker":
		return "", "", `{"animated":true}`
	case "location":
		return "", "", `{"lat":24.7136,"lon":46.6753,"name":"Riyadh","address":"King Fahd Road"}`
	case "live_location":
		return "", body, `{"lat":24.7136,"lon":46.6753,"accuracy_m":8,"speed":3,"sequence":2}`
	case "contact":
		return "Layla", "", `{"display_name":"Layla","vcard":"BEGIN:VCARD\\nFN:Layla\\nEND:VCARD"}`
	case "contacts":
		return "Family", "", `{"display_name":"Family","contacts":[{"display_name":"Layla","vcard":"VCARD"},{"display_name":"Omar","vcard":"VCARD"}]}`
	case "poll":
		return "Lunch?", "", `{"question":"Lunch?","options":["Pizza","شَاوَرْمَا"],"max":1}`
	case "event":
		return "Picnic", "", `{"name":"Picnic","description":"Synthetic event","start_ts":1761440000,"end_ts":1761443600,"location":{"name":"Park","lat":24.7,"lon":46.6},"canceled":false}`
	case "system":
		return body, "", `{"type":"callLogMesssage"}`
	case "unknown":
		return body, "", `{"fields":["futureMessage"]}`
	default:
		return body, "", `{}`
	}
}

func mediaKind(kind string) bool {
	switch kind {
	case "image", "video", "audio", "voice", "document", "sticker", "gif":
		return true
	default:
		return false
	}
}

func mediaMIME(kind string) string {
	switch kind {
	case "image", "sticker":
		return "image/jpeg"
	case "video", "gif":
		return "video/mp4"
	case "audio", "voice":
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}

func (g *generator) addGaps(ctx context.Context, pipe *ingest.Pipeline) error {
	for i := 0; i < g.opts.Chats; i++ {
		lid, _ := chatIDs(i)
		ts := g.base + int64(g.opts.Messages+i+1)
		target := fmt.Sprintf("MISSING-%03d", i)
		event := &store.MessageEvent{
			ChatJID: lid, TargetID: target, Kind: "undecryptable", EventID: "GAP-" + target,
			Cause:  []string{"no_sender_key", "untrusted_identity", "view_once_unavailable", "other"}[i%4],
			Source: "live", Timestamp: ts, ReceivedAt: ts, RawProto: []byte("synthetic-gap:" + target),
		}
		result := ingest.Result{Kind: ingest.KindUndecryptable, Chat: store.Chat{JID: lid}, Event: event, Received: time.Unix(ts, 0)}
		if err := pipe.Ingest(ctx, result); err != nil {
			return fmt.Errorf("ingest synthetic gap: %w", err)
		}
	}
	return nil
}
