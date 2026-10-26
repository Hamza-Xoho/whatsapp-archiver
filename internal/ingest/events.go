package ingest

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

// normalizeEvent turns a message that is about another message (reaction, poll vote, pin, edit, revoke, other protocol
// messages) into an event; it reports whether it did. r.Message is dropped then: such a message is not a row of messages.
func normalizeEvent(msg *waE2E.Message, r *Result) bool {
	if x := msg.GetReactionMessage(); x != nil {
		r.Kind = KindReaction
		r.Event = event(r, x.GetKey().GetID(), "reaction", x.GetText())
	}
	if x := msg.GetPollUpdateMessage(); x != nil {
		r.Kind = KindPollVote
		r.Event = event(r, x.GetPollCreationMessageKey().GetID(), "poll_vote", "")
	}
	if x := msg.GetPinInChatMessage(); x != nil {
		r.Kind = KindPin
		r.Event = event(r, x.GetKey().GetID(), "pin", fmt.Sprint(x.GetType()))
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
	}
	if r.Event == nil {
		return false
	}
	r.Message, r.Media = nil, nil
	return true
}

// event describes a message that is about message target. Its own ID is its source_id (R11). A reaction or a vote is
// identified by that ID, so the copy of it in a history chunk is the same row; every other event by its content.
func event(r *Result, target, kind, value string) *store.MessageEvent {
	own := r.Message.ID
	id := stableID(r.Chat.JID, target, kind, string(r.Message.RawProto))
	if own != "" && (kind == "reaction" || kind == "poll_vote") {
		id = stableID(r.Chat.JID, target, kind, own)
	}
	return &store.MessageEvent{ChatJID: r.Chat.JID, TargetID: target, Kind: kind, EventID: id, SourceID: own, SenderJID: r.Sender.JID, Source: r.Message.Source,
		Timestamp: r.Message.Timestamp, ReceivedAt: r.Received.Unix(), NewText: value, Value: value, RawProto: r.Message.RawProto}
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

// undecryptableCause says why a message could not be decrypted, as far as whatsmeow tells: a view-once message the
// sender did not deliver, a message that came without ciphertext or without the group's sender key, or something else.
// The event carries no error, so an untrusted identity is not told apart (prior-art.md); that cause is never produced.
func undecryptableCause(evt *events.UndecryptableMessage) string {
	switch {
	case evt.UnavailableType == events.UnavailableTypeViewOnce:
		return "view_once_unavailable"
	case evt.UnavailableType != events.UnavailableTypeUnknown:
		return "other" // an unavailable type this build does not know says nothing about a sender key
	case evt.IsUnavailable:
		return "no_sender_key"
	}
	return "other"
}

func FromUndecryptable(evt *events.UndecryptableMessage, received time.Time) Result {
	if evt == nil {
		return Result{Received: received}
	}
	chat := evt.Info.Chat.String()
	id := string(evt.Info.ID)
	e := store.MessageEvent{ChatJID: chat, TargetID: id, Kind: string(KindUndecryptable), EventID: stableID(chat, id, string(KindUndecryptable)), Cause: undecryptableCause(evt),
		SenderJID: evt.Info.Sender.String(), Source: "live", Timestamp: evt.Info.Timestamp.Unix(), ReceivedAt: received.Unix(), Value: string(evt.DecryptFailMode)}
	return Result{Kind: KindUndecryptable, Chat: store.Chat{JID: chat}, Sender: store.Participant{JID: evt.Info.Sender.String()}, Event: &e, FromMe: evt.Info.IsFromMe, Received: received}
}

// FromStub normalises a history message with no body (a revoke, a group change, a ciphertext marker). It is a stub
// event under its key ID, never a message row. Its raw proto is the WebMessageInfo without key material (stripMessage), which
// is never stored (archive.db is backed up).
func FromStub(evt *events.Message, web *waWeb.WebMessageInfo, received time.Time) Result {
	info := evt.Info
	chat, sender, id := info.Chat.String(), info.Sender.String(), string(info.ID)
	clean := proto.Clone(web).(*waWeb.WebMessageInfo)
	stripMessage(clean.ProtoReflect())
	raw, _ := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(clean)
	e := store.MessageEvent{ChatJID: chat, TargetID: id, Kind: string(KindStub), EventID: id, SourceID: id, SenderJID: sender, Source: "history", Timestamp: info.Timestamp.Unix(),
		ReceivedAt: received.Unix(), Value: web.GetMessageStubType().String(), RawProto: raw}
	return Result{Kind: KindStub, Chat: store.Chat{JID: chat}, Sender: store.Participant{JID: sender, PushName: info.PushName}, Event: &e, Mappings: messageMappings(info, received), FromMe: info.IsFromMe, Received: received}
}

// ---- reactions and poll updates embedded in a history message (R14) ----

// embeddedEvents makes the reaction and poll_vote events of the reactions and poll updates a history message carries.
// Each is parsed as the message WhatsApp would have delivered: a reaction or poll update message under its own key,
// about the parent. parent is the parsed parent (nil for a stub). Their raw proto is the history record itself.
func (p *Pipeline) embeddedEvents(chat types.JID, web *waWeb.WebMessageInfo, parent *events.Message, now time.Time) []Result {
	var out []Result
	target := web.GetKey()
	for _, rx := range web.GetReactions() {
		msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: target, Text: proto.String(rx.GetText()), SenderTimestampMS: proto.Int64(rx.GetSenderTimestampMS())}}
		if r, ok := p.embedded(chat, web, rx.GetKey(), rx.GetSenderTimestampMS(), msg, rx, now); ok {
			out = append(out, r)
		}
	}
	for _, pu := range web.GetPollUpdates() {
		msg := &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: target, SenderTimestampMS: proto.Int64(pu.GetSenderTimestampMS())}}
		r, ok := p.embedded(chat, web, pu.GetPollUpdateMessageKey(), pu.GetSenderTimestampMS(), msg, pu, now)
		if !ok {
			continue
		}
		if parent != nil && pu.GetVote() != nil { // no vote: nothing to name, unlike a vote that selects nothing
			if poll := pollOf(parent.Message); poll != nil {
				r.Event.Value = voteValue(pollOptions(poll), pu.GetVote().GetSelectedOptions())
			}
		}
		out = append(out, r)
	}
	return out
}

func (p *Pipeline) embedded(chat types.JID, web *waWeb.WebMessageInfo, own *waCommon.MessageKey, ms int64, msg *waE2E.Message, record proto.Message, now time.Time) (Result, bool) {
	if own.GetID() == "" {
		return Result{}, false
	}
	synthetic := &waWeb.WebMessageInfo{Key: own, MessageTimestamp: proto.Uint64(web.GetMessageTimestamp()), Message: msg}
	if ms > 0 {
		synthetic.MessageTimestamp = proto.Uint64(uint64(ms / 1000))
	}
	evt, err := p.parse(chat, synthetic)
	if err != nil {
		p.log.Warn("history reaction or poll update not parsed", "chat", chat.String(), "id", own.GetID(), "err", err)
		return Result{}, false
	}
	r := FromMessage(evt, "history", now)
	if r.Event == nil {
		return Result{}, false
	}
	if raw, err := (proto.MarshalOptions{Deterministic: true, AllowPartial: true}).Marshal(record); err == nil {
		r.Event.RawProto = raw
	}
	return r, true
}

// voteValue names the options a vote selected, in the order selected, by the SHA-256 of their names. A selection that
// names no option of the poll is an empty value; a vote that selects nothing (a retracted vote) is an empty list.
func voteValue(options []string, selected [][]byte) string {
	names := make([]string, 0, len(selected))
	for _, hash := range selected {
		for _, name := range options {
			if sum := sha256.Sum256([]byte(name)); bytes.Equal(sum[:], hash) {
				names = append(names, name)
				break
			}
		}
	}
	if len(selected) > 0 && len(names) == 0 {
		return ""
	}
	value, err := store.MarshalDetails(struct {
		Options []string `json:"options"`
	}{names})
	if err != nil {
		return ""
	}
	return value
}
