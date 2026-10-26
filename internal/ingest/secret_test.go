package ingest_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
)

// A message's context carries the secret its reactions and poll votes are encrypted with. That is key material and
// archive.db is backed up, so the stored raw proto never holds it, neither on the message nor inside a wrapper.
func TestRawProtoOfMessageHoldsNoMessageSecret(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	withSecret := func() *waE2E.Message {
		return &waE2E.Message{
			Conversation:       proto.String("hello"),
			MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret, BotMessageSecret: secret},
		}
	}
	cases := map[string]*waE2E.Message{
		"plain":     withSecret(),
		"ephemeral": {EphemeralMessage: &waE2E.FutureProofMessage{Message: withSecret()}},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			r := ingest.FromMessage(messageEvent("1@lid", "A", msg), "live", time.Unix(20, 0))
			var stored waE2E.Message
			if err := proto.Unmarshal(r.Message.RawProto, &stored); err != nil {
				t.Fatal(err)
			}
			check := stored.GetMessageContextInfo()
			if name == "ephemeral" {
				check = stored.GetEphemeralMessage().GetMessage().GetMessageContextInfo()
			}
			if len(check.GetMessageSecret()) != 0 || len(check.GetBotMessageSecret()) != 0 {
				t.Fatalf("stored raw proto still holds a message secret: %v", check)
			}
			if name == "plain" && stored.GetConversation() != "hello" {
				t.Fatalf("the body was lost: %q", stored.GetConversation())
			}
		})
	}
}

// An edit carries the new content in ProtocolMessage.EditedMessage, and that message has its own context info. Its
// secret is key material too, so it is cleared there, in the bare form and inside the EditedMessage wrapper.
func TestRawProtoOfEditHoldsNoMessageSecret(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	edited := func() *waE2E.Message {
		return &waE2E.Message{
			Conversation:       proto.String("new text"),
			MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret, BotMessageSecret: secret},
		}
	}
	edit := func() *waE2E.Message {
		return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Key: &waCommon.MessageKey{ID: proto.String("T")}, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: edited()}}
	}
	cases := map[string]*waE2E.Message{
		"bare":    edit(),
		"wrapped": {EditedMessage: &waE2E.FutureProofMessage{Message: edit()}},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			r := ingest.FromMessage(messageEvent("1@lid", "ED", msg), "live", time.Unix(20, 0))
			if r.Event == nil || r.Event.Kind != "edit" || r.Event.NewText != "new text" {
				t.Fatalf("event = %+v, want an edit to %q", r.Event, "new text")
			}
			var stored waE2E.Message
			if err := proto.Unmarshal(r.Event.RawProto, &stored); err != nil {
				t.Fatal(err)
			}
			pm := stored.GetProtocolMessage()
			if name == "wrapped" {
				pm = stored.GetEditedMessage().GetMessage().GetProtocolMessage()
			}
			ci := pm.GetEditedMessage().GetMessageContextInfo()
			if pm.GetEditedMessage().GetConversation() != "new text" {
				t.Fatalf("the edited body was lost: %v", pm)
			}
			if len(ci.GetMessageSecret()) != 0 || len(ci.GetBotMessageSecret()) != 0 {
				t.Fatalf("stored raw proto of the edit still holds a message secret: %v", ci)
			}
		})
	}
}
