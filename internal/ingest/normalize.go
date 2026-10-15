package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	raw, _ := proto.Marshal(rawMsg)
	if raw == nil {
		raw = []byte{} // a bodyless message is stored with an empty raw_proto; the column is NOT NULL
	}
	info := evt.Info
	chat := info.Chat.String()
	sender := info.Sender.String()
	m := store.Message{ChatJID: chat, ID: string(info.ID), SenderJID: sender, Timestamp: info.Timestamp.Unix(), ReceivedAt: received.Unix(), Source: source, RawProto: raw}
	r := Result{Kind: KindMessage, Chat: store.Chat{JID: chat}, Sender: store.Participant{JID: sender, DisplayName: info.PushName}, Message: &m, FromMe: info.IsFromMe, Received: received}
	normalizeContent(evt.Message, &r)
	r.Mappings = messageMappings(info, received)
	return r
}

func normalizeContent(msg *waE2E.Message, r *Result) {
	if msg == nil {
		return
	}
	if inner := msg.GetEditedMessage().GetMessage(); inner != nil {
		msg = inner // the EditedMessage wrapper (whatsmeow's BuildEdit) holds the MESSAGE_EDIT protocol message
	}
	if text := msg.GetConversation(); text != "" {
		r.Message.Text = text
	}
	if x := msg.GetExtendedTextMessage(); x != nil {
		r.Message.Text = x.GetText()
		contextFields(r.Message, x.GetContextInfo())
	}
	if x := msg.GetImageMessage(); x != nil {
		r.Message.Text = x.GetCaption()
		contextFields(r.Message, x.GetContextInfo())
		setMedia(r, "image", x.GetMimetype(), x.GetFileLength())
	}
	if x := msg.GetDocumentMessage(); x != nil {
		r.Message.Text = x.GetCaption()
		contextFields(r.Message, x.GetContextInfo())
		setMedia(r, "document", x.GetMimetype(), x.GetFileLength())
	}
	if x := msg.GetAudioMessage(); x != nil {
		kind := "audio"
		if x.GetPTT() {
			kind = "audio"
		}
		setMedia(r, kind, x.GetMimetype(), x.GetFileLength())
	}
	if x := msg.GetVideoMessage(); x != nil {
		kind := "video"
		if x.GetGifPlayback() {
			kind = "gif"
		}
		r.Message.Text = x.GetCaption()
		contextFields(r.Message, x.GetContextInfo())
		setMedia(r, kind, x.GetMimetype(), x.GetFileLength())
	}
	if x := msg.GetStickerMessage(); x != nil {
		setMedia(r, "sticker", x.GetMimetype(), x.GetFileLength())
	}
	if x := msg.GetReactionMessage(); x != nil {
		r.Kind = KindReaction
		r.Event = event(r, x.GetKey().GetID(), "reaction", x.GetText())
		r.Message = nil
		r.Media = nil
	}
	if x := msg.GetPollUpdateMessage(); x != nil {
		r.Kind = KindPollVote
		r.Event = event(r, x.GetPollCreationMessageKey().GetID(), "poll_vote", "")
		r.Message = nil
		r.Media = nil
	}
	if x := msg.GetPinInChatMessage(); x != nil {
		r.Kind = KindPin
		r.Event = event(r, x.GetKey().GetID(), "pin", fmt.Sprint(x.GetType()))
		r.Message = nil
		r.Media = nil
	}
	if x := msg.GetSecretEncryptedMessage(); x != nil {
		// The content is encrypted with the message secret; target and raw proto are kept. A MESSAGE_EDIT is an edit;
		// every other type (poll edit, event edit, ...) is a secret_encrypted event named by its type.
		target := x.GetTargetMessageKey().GetID()
		if x.GetSecretEncType() == waE2E.SecretEncryptedMessage_MESSAGE_EDIT {
			r.Kind = KindEdit
			r.Event = event(r, target, "edit", "")
		} else {
			r.Kind = KindSecretEncrypted
			r.Event = event(r, target, string(KindSecretEncrypted), x.GetSecretEncType().String())
			r.Event.NewText = ""
		}
		r.Message = nil
		r.Media = nil
	}
	if x := msg.GetProtocolMessage(); x != nil {
		target := x.GetKey().GetID()
		switch {
		case x.Type == nil: // the proto2 default is REVOKE; an untyped protocol message deletes nothing
			r.Kind = KindProtocol
			r.Event = event(r, target, string(KindProtocol), "")
		case x.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT:
			r.Kind = KindEdit
			r.Event = event(r, target, "edit", messageText(x.GetEditedMessage()))
		case x.GetType() == waE2E.ProtocolMessage_REVOKE:
			r.Kind = KindRevoke
			r.Event = event(r, target, "revoke", "")
		default:
			r.Kind = KindProtocol
			r.Event = event(r, target, string(KindProtocol), x.GetType().String())
			r.Event.NewText = ""
			if keyMaterial[x.GetType()] {
				r.Event.RawProto = nil
			}
		}
		r.Message = nil
		r.Media = nil
	}
}

func contextFields(m *store.Message, c *waE2E.ContextInfo) {
	if c == nil {
		return
	}
	m.QuotedID = c.GetStanzaID()
	m.Forwarded = c.GetForwardingScore() > 0
}
func setMedia(r *Result, kind, mime string, size uint64) {
	r.Message.MediaType = kind
	r.Media = &store.Media{ChatJID: r.Message.ChatJID, MessageID: r.Message.ID, SenderJID: r.Message.SenderJID, Source: r.Message.Source, Type: kind, MIME: mime, DeclaredSize: int64(size), RawProto: r.Message.RawProto, State: "pending", MessageTimestamp: r.Message.Timestamp, FromMe: r.FromMe}
}
func event(r *Result, target, kind, value string) *store.MessageEvent {
	return &store.MessageEvent{ChatJID: r.Chat.JID, TargetID: target, Kind: kind, EventID: stableID(r.Chat.JID, target, kind, string(r.Message.RawProto)), SenderJID: r.Sender.JID, Timestamp: r.Message.Timestamp, ReceivedAt: r.Received.Unix(), NewText: value, Value: value, RawProto: r.Message.RawProto}
}
func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if m.GetConversation() != "" {
		return m.GetConversation()
	}
	if x := m.GetExtendedTextMessage(); x != nil {
		return x.GetText()
	}
	if x := m.GetImageMessage(); x != nil {
		return x.GetCaption()
	}
	if x := m.GetVideoMessage(); x != nil {
		return x.GetCaption()
	}
	return m.GetDocumentMessage().GetCaption()
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

func FromUndecryptable(evt *events.UndecryptableMessage, received time.Time) Result {
	if evt == nil {
		return Result{Received: received}
	}
	chat := evt.Info.Chat.String()
	id := string(evt.Info.ID)
	e := store.MessageEvent{ChatJID: chat, TargetID: id, Kind: string(KindUndecryptable), EventID: stableID(chat, id, string(KindUndecryptable)), SenderJID: evt.Info.Sender.String(), Timestamp: evt.Info.Timestamp.Unix(), ReceivedAt: received.Unix(), Value: string(evt.DecryptFailMode)}
	return Result{Kind: KindUndecryptable, Chat: store.Chat{JID: chat}, Sender: store.Participant{JID: evt.Info.Sender.String()}, Event: &e, FromMe: evt.Info.IsFromMe, Received: received}
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
