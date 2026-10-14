package ingest_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
)

func TestPlainTextNormalizes(t *testing.T) {
	e := messageEvent("1@lid", "A", &waE2E.Message{Conversation: proto.String("hello")})
	r := ingest.FromMessage(e, "live", time.Unix(20, 0))
	if r.Kind != ingest.KindMessage || r.Message.Text != "hello" || r.Message.Timestamp != 10 || len(r.Message.RawProto) == 0 {
		t.Fatalf("result=%+v", r)
	}
}

func TestImageAndStickerMedia(t *testing.T) {
	image := messageEvent("1@lid", "I", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}})
	if r := ingest.FromMessage(image, "live", time.Now()); r.Media == nil || r.Media.Type != "image" || r.Media.MIME != "image/jpeg" {
		t.Fatalf("image=%+v", r)
	}
	sticker := messageEvent("1@lid", "S", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}})
	if r := ingest.FromMessage(sticker, "live", time.Now()); r.Media == nil || r.Media.Type != "sticker" {
		t.Fatalf("sticker=%+v", r)
	}
}

func TestReplyAndForwarded(t *testing.T) {
	e := messageEvent("1@lid", "A", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("reply"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q"), ForwardingScore: proto.Uint32(1)}}})
	r := ingest.FromMessage(e, "live", time.Now())
	if r.Message.QuotedID != "Q" || !r.Message.Forwarded {
		t.Fatalf("message=%+v", r.Message)
	}
}

func TestUndecryptableIsDeterministic(t *testing.T) {
	e := &events.UndecryptableMessage{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: jid("1@lid")}, ID: "X"}, DecryptFailMode: events.DecryptFailHide}
	a := ingest.FromUndecryptable(e, time.Unix(4, 0))
	b := ingest.FromUndecryptable(e, time.Unix(5, 0))
	if a.Kind != ingest.KindUndecryptable || a.Event.TargetID != "X" || a.Event.EventID != b.Event.EventID || a.Event.Value != "hide" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func TestAlternateJIDCreatesMapping(t *testing.T) {
	e := messageEvent("123@lid", "A", &waE2E.Message{Conversation: proto.String("x")})
	e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	r := ingest.FromMessage(e, "live", time.Now())
	if len(r.Mappings) != 1 || r.Mappings[0].LID != "123@lid" {
		t.Fatalf("mappings=%+v", r.Mappings)
	}
}

func messageEvent(chat, id string, msg *waE2E.Message) *events.Message {
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: jid(chat), Sender: jid("2@lid")}, ID: types.MessageID(id), Timestamp: time.Unix(10, 0)}, Message: msg, RawMessage: msg}
}
func jid(value string) types.JID {
	parsed, err := types.ParseJID(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
