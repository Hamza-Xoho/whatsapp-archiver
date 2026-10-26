package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

func FromMessage(evt *events.Message, source string, received time.Time) Result {
	if evt == nil {
		return Result{Received: received}
	}
	rawMsg := evt.RawMessage
	if rawMsg == nil {
		rawMsg = evt.Message
	}
	raw, _ := proto.Marshal(withoutMessageSecrets(rawMsg))
	if raw == nil {
		raw = []byte{} // a bodyless message is stored with an empty raw_proto; the column is NOT NULL
	}
	info := evt.Info
	chat := info.Chat.String()
	sender := info.Sender.String()
	m := store.Message{ChatJID: chat, ID: string(info.ID), SenderJID: sender, FromMe: info.IsFromMe, PushName: info.PushName, Timestamp: info.Timestamp.Unix(), ReceivedAt: received.Unix(), Source: source, RawProto: raw}
	r := Result{Kind: KindMessage, Chat: store.Chat{JID: chat}, Sender: store.Participant{JID: sender, PushName: info.PushName}, Message: &m, FromMe: info.IsFromMe, Received: received}
	normalizeContent(evt, rawMsg, &r)
	if keyOnly(rawMsg) {
		r.Kind = KindKeyOnly
	}
	r.Mappings = messageMappings(info, received)
	return r
}

// keyOnly reports whether msg holds a sender key distribution and nothing else but context info. A group message
// from a sender this device has no key for arrives as such a part and then the content part, with the same ID; storing
// the key-only part would take the (chat, id) slot and the content would be dropped as a duplicate.
func keyOnly(msg *waE2E.Message) bool {
	if msg.GetSenderKeyDistributionMessage() == nil {
		return false
	}
	rest := proto.Clone(msg).(*waE2E.Message)
	rest.SenderKeyDistributionMessage = nil
	rest.MessageContextInfo = nil
	return proto.Size(rest) == 0
}

// withoutMessageSecrets returns a copy of msg without key material at any depth: the message secrets in every context
// (including quoted, device-sent, wrapped and edited messages), a poll's encKey, sender keys and the payloads of the
// keyMaterial protocol messages (stripMessage, the one rule of secrets.go). The raw proto is stored in the backed-up,
// insert-only archive, and an event's identity hashes it, so live and stripped history copies must clear the same way.
func withoutMessageSecrets(msg *waE2E.Message) *waE2E.Message {
	if msg == nil {
		return nil
	}
	clean := proto.Clone(msg).(*waE2E.Message)
	stripMessage(clean.ProtoReflect())
	return clean
}

// keyMaterial lists the protocol message types whose payload holds keys: app-state sync keys, contact hash keys,
// and the media key of a history blob or of a peer-data response. They are stored without raw proto, because
// archive.db is backed up and keys must never be.
var keyMaterial = map[waE2E.ProtocolMessage_Type]bool{
	waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE:                     true,
	waE2E.ProtocolMessage_SHARED_DEVICE_CONTACT_HASH_KEY_SHARE:         true,
	waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION:                    true,
	waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE: true,
}

func messageMappings(info types.MessageInfo, seen time.Time) []store.IDMapping {
	pairs := [][2]types.JID{{info.Chat, info.RecipientAlt}, {info.Sender, info.SenderAlt}}
	var out []store.IDMapping
	for _, pair := range pairs {
		a, b := pair[0], pair[1]
		if a.IsEmpty() || b.IsEmpty() {
			continue
		}
		var lid, pn types.JID
		if a.Server == types.HiddenUserServer {
			lid, pn = a, b
		} else if b.Server == types.HiddenUserServer {
			lid, pn = b, a
		} else {
			continue
		}
		out = append(out, store.IDMapping{LID: lid.String(), PN: pn.String(), Source: "message_alt", FirstSeen: seen})
	}
	return out
}
func stableID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}
