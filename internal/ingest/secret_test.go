package ingest_test

import (
	"testing"
	"time"

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
