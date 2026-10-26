package ingest_test

import (
	"bytes"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
)

// I4 (Hamza's ruling, 26 Oct 2026): "keys never stored" holds for the live path too. messages and message_events are
// insert-only, so a secret written to raw_proto once can never be scrubbed. FromMessage clears the same key material
// as the spool strip, at any depth: device-sent, quoted, wrapped and edited messages, a poll's encKey.

type planter struct{ planted [][]byte }

func (p *planter) secret() []byte {
	s := bytes.Repeat([]byte{0xC0 + byte(len(p.planted))}, 32)
	p.planted = append(p.planted, s)
	return s
}

func (p *planter) ctx() *waE2E.MessageContextInfo {
	return &waE2E.MessageContextInfo{MessageSecret: p.secret(), BotMessageSecret: p.secret(), DeviceListMetadataVersion: proto.Int32(2)}
}

func (p *planter) hello() *waE2E.Message {
	return &waE2E.Message{Conversation: proto.String("hello"), MessageContextInfo: p.ctx()}
}

var editTarget = &waCommon.MessageKey{ID: proto.String("TARGET"), RemoteJID: proto.String("g@g.us")}

func (p *planter) cases() map[string]*waE2E.Message {
	return map[string]*waE2E.Message{
		"device sent": {DeviceSentMessage: &waE2E.DeviceSentMessage{DestinationJID: proto.String("g@g.us"), Message: p.hello()}, MessageContextInfo: p.ctx()},
		"quoted": {ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("reply"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("TARGET"), QuotedMessage: p.hello()}},
			MessageContextInfo: p.ctx()},
		"poll enc key": {PollCreationMessage: &waE2E.PollCreationMessage{EncKey: p.secret(), Name: proto.String("lunch?"),
			Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("pizza")}}}, MessageContextInfo: p.ctx()},
		"ephemeral":        {EphemeralMessage: &waE2E.FutureProofMessage{Message: p.hello()}},
		"view once":        {ViewOnceMessage: &waE2E.FutureProofMessage{Message: p.hello()}},
		"view once v2":     {ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: p.hello()}},
		"document caption": {DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("c")}, MessageContextInfo: p.ctx()}}},
		"edit with a quote in the edited message": {ProtocolMessage: &waE2E.ProtocolMessage{Key: editTarget, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			EditedMessage: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("fixed"), ContextInfo: &waE2E.ContextInfo{QuotedMessage: p.hello()}},
				MessageContextInfo: p.ctx()}}, MessageContextInfo: p.ctx()},
		"edit inside device sent": {DeviceSentMessage: &waE2E.DeviceSentMessage{DestinationJID: proto.String("g@g.us"), Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Key: editTarget, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: p.hello()}, MessageContextInfo: p.ctx()}}},
		"wrapped edit": {EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Key: editTarget, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: p.hello()}, MessageContextInfo: p.ctx()}}},
		"reaction":        {ReactionMessage: &waE2E.ReactionMessage{Key: editTarget, Text: proto.String("+1")}, MessageContextInfo: p.ctx()},
		"sender key part": {SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{GroupID: proto.String("g@g.us"), AxolotlSenderKeyDistributionMessage: p.secret()}, Conversation: proto.String("x")},
	}
}

func TestFromMessageStoresNoSecretInRawProto(t *testing.T) {
	cases := (&planter{}).cases() // names only; each case below plants its own secrets
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			p := &planter{}
			msg := p.cases()[name]
			r := ingest.FromMessage(messageEvent("g@g.us", "M1", msg), "live", time.Unix(20, 0))
			var raw []byte
			switch {
			case r.Message != nil:
				raw = r.Message.RawProto
			case r.Event != nil:
				raw = r.Event.RawProto
			default:
				t.Fatalf("kind %q made neither a message nor an event", r.Kind)
			}
			if len(raw) == 0 && name != "sender key part" {
				t.Fatal("raw_proto is empty")
			}
			for i, s := range p.planted {
				if bytes.Contains(raw, s) {
					t.Errorf("planted secret #%d of %d is in raw_proto", i, len(p.planted))
				}
			}
		})
	}
}

// The identity of an edit, a revoke or another protocol event is a hash of its raw proto, so a live copy and the copy in
// a stripped history chunk must have the same raw proto: one clearing rule on both paths.
func TestLiveAndStrippedHistoryEventsHaveTheSameEventID(t *testing.T) {
	notification := func(p *planter) *waE2E.Message {
		return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
			HistorySyncNotification: &waE2E.HistorySyncNotification{MediaKey: p.secret(), DirectPath: proto.String("/blob")}}}
	}
	for name, build := range map[string]func(*planter) *waE2E.Message{
		"edit with a quote": func(p *planter) *waE2E.Message { return p.cases()["edit with a quote in the edited message"] },
		"wrapped edit":      func(p *planter) *waE2E.Message { return p.cases()["wrapped edit"] },
		"revoke": func(p *planter) *waE2E.Message {
			return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: editTarget, Type: waE2E.ProtocolMessage_REVOKE.Enum()}, MessageContextInfo: p.ctx()}
		},
		"history notification": notification,
	} {
		t.Run(name, func(t *testing.T) {
			p := &planter{}
			msg := build(p)
			hs := &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{{ID: proto.String("g@g.us"),
				Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{ID: proto.String("M1")}, Message: proto.Clone(msg).(*waE2E.Message)}}}}}}
			stripped := ingest.StripHistorySync(hs).GetConversations()[0].GetMessages()[0].GetMessage().GetMessage()
			live := ingest.FromMessage(messageEvent("g@g.us", "M1", msg), "live", time.Unix(20, 0))
			hist := ingest.FromMessage(messageEvent("g@g.us", "M1", stripped), "history", time.Unix(30, 0))
			if live.Event == nil || hist.Event == nil || live.Event.EventID == "" || live.Event.EventID != hist.Event.EventID {
				t.Fatalf("event ids: live %v, stripped history %v; want equal and non-empty", live.Event, hist.Event)
			}
		})
	}
}
