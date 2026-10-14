package ingest_test

import (
	"slices"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/testkit"
)

func protocolMsg(typ *waE2E.ProtocolMessage_Type, target string) *waE2E.ProtocolMessage {
	pm := &waE2E.ProtocolMessage{Type: typ}
	if target != "" {
		pm.Key = &waCommon.MessageKey{ID: proto.String(target), RemoteJID: proto.String("g@g.us")}
	}
	return pm
}

// H8 / Table 1: an edit arrives as a bare MESSAGE_EDIT protocol message (old) or wrapped in EditedMessage (new,
// what whatsmeow's BuildEdit sends); both, and the new shape after UnwrapRaw, are edits of the original message.
func TestProtocolEditOldAndNewFormats(t *testing.T) {
	edit := func(content *waE2E.Message) *waE2E.ProtocolMessage {
		pm := protocolMsg(waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), "ORIG")
		pm.EditedMessage = content
		pm.TimestampMS = proto.Int64(11000)
		return pm
	}
	old := &waE2E.Message{ProtocolMessage: edit(text("old format"))}
	wrapped := &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: edit(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("new format")}})}}}
	unwrapped := messageEvent("g@g.us", "E3", nil)
	unwrapped.RawMessage = wrapped
	unwrapped.UnwrapRaw()
	if !unwrapped.IsEdit {
		t.Fatal("UnwrapRaw did not mark the EditedMessage wrapper as an edit")
	}
	cases := []struct {
		name string
		evt  *events.Message
		want string
	}{
		{"old", messageEvent("g@g.us", "E1", old), "old format"},
		{"new", messageEvent("g@g.us", "E2", wrapped), "new format"},
		{"new unwrapped", unwrapped, "new format"},
	}
	for _, c := range cases {
		r := ingest.FromMessage(c.evt, "live", time.Unix(20, 0))
		if r.Kind != ingest.KindEdit || r.Message != nil || r.Event == nil {
			t.Fatalf("%s: kind=%s message=%+v event=%+v", c.name, r.Kind, r.Message, r.Event)
		}
		e := r.Event
		if e.Kind != "edit" || e.TargetID != "ORIG" || e.NewText != c.want || e.Timestamp != 10 || len(e.RawProto) == 0 {
			t.Fatalf("%s: event=%+v", c.name, e)
		}
	}
}

// H8: protocol messages that are not revokes (sent in volume at pairing) are kept as "protocol" events, never revokes.
func TestNonRevokeProtocolMessageIsNotARevoke(t *testing.T) {
	cases := []struct {
		pm     *waE2E.ProtocolMessage
		target string
		value  string
		keys   bool // carries key material: stored without raw proto (M6)
	}{
		{protocolMsg(waE2E.ProtocolMessage_EPHEMERAL_SETTING.Enum(), ""), "", "EPHEMERAL_SETTING", false},
		{protocolMsg(waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(), ""), "", "HISTORY_SYNC_NOTIFICATION", true},
		{protocolMsg(waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum(), ""), "", "APP_STATE_SYNC_KEY_SHARE", true},
		{protocolMsg(waE2E.ProtocolMessage_MESSAGE_UNSCHEDULE.Enum(), "SCHED"), "SCHED", "MESSAGE_UNSCHEDULE", false},
		// No type at all: the proto2 default (REVOKE) must not turn it into a deletion.
		{protocolMsg(nil, "T"), "T", "", false},
	}
	for _, c := range cases {
		c.pm.EphemeralExpiration = proto.Uint32(86400)
		r := ingest.FromMessage(messageEvent("g@g.us", "PM", &waE2E.Message{ProtocolMessage: c.pm}), "live", time.Unix(20, 0))
		if r.Kind != ingest.Kind("protocol") || r.Message != nil || r.Event == nil {
			t.Fatalf("%s: kind=%s message=%+v event=%+v", c.value, r.Kind, r.Message, r.Event)
		}
		if e := r.Event; e.Kind != "protocol" || e.TargetID != c.target || e.Value != c.value || e.NewText != "" || (len(e.RawProto) == 0) != c.keys {
			t.Fatalf("%s: event=%+v", c.value, e)
		}
	}
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	p.Handle(messageEvent("g@g.us", "PM1", &waE2E.Message{ProtocolMessage: protocolMsg(waE2E.ProtocolMessage_EPHEMERAL_SETTING.Enum(), "")}))
	if got := rows(t, st, `SELECT kind,target_id,value FROM message_events`); !slices.Equal(got, []string{"protocol||EPHEMERAL_SETTING"}) {
		t.Fatalf("message_events=%v", got)
	}
}

func TestRevokeNormalizes(t *testing.T) {
	e := messageEvent("g@g.us", "R", &waE2E.Message{ProtocolMessage: protocolMsg(waE2E.ProtocolMessage_REVOKE.Enum(), "GONE")})
	a := ingest.FromMessage(e, "live", time.Unix(20, 0))
	b := ingest.FromMessage(e, "live", time.Unix(30, 0))
	if a.Kind != ingest.KindRevoke || a.Message != nil || a.Event == nil {
		t.Fatalf("kind=%s message=%+v event=%+v", a.Kind, a.Message, a.Event)
	}
	if ev := a.Event; ev.Kind != "revoke" || ev.TargetID != "GONE" || ev.SenderJID != "2@lid" || ev.Timestamp != 10 || ev.ReceivedAt != 20 || len(ev.RawProto) == 0 || ev.EventID != b.Event.EventID {
		t.Fatalf("a=%+v b=%+v", a.Event, b.Event)
	}
}

func TestReactionNormalizes(t *testing.T) {
	msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("LIKED")}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(10500)}}
	r := ingest.FromMessage(messageEvent("g@g.us", "RX", msg), "live", time.Unix(20, 0))
	if r.Kind != ingest.KindReaction || r.Message != nil || r.Event == nil {
		t.Fatalf("kind=%s message=%+v event=%+v", r.Kind, r.Message, r.Event)
	}
	if e := r.Event; e.Kind != "reaction" || e.TargetID != "LIKED" || e.Value != "👍" || e.Timestamp != 10 || len(e.RawProto) == 0 {
		t.Fatalf("event=%+v", e)
	}
}

func TestPinNormalizes(t *testing.T) {
	msg := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{Key: &waCommon.MessageKey{ID: proto.String("PINNED")}, Type: waE2E.PinInChatMessage_PIN_FOR_ALL.Enum()}}
	r := ingest.FromMessage(messageEvent("g@g.us", "PX", msg), "live", time.Unix(20, 0))
	if r.Kind != ingest.KindPin || r.Message != nil || r.Event == nil {
		t.Fatalf("kind=%s message=%+v event=%+v", r.Kind, r.Message, r.Event)
	}
	if e := r.Event; e.Kind != "pin" || e.TargetID != "PINNED" || e.Value != "PIN_FOR_ALL" || len(e.RawProto) == 0 {
		t.Fatalf("event=%+v", e)
	}
}

// Table 1: newer poll versions (PollCreationMessageV2..V6 in the pinned proto) are still voted on with
// PollUpdateMessage; a vote on a current and on a V5 poll is a poll_vote event on the poll, with its encrypted raw proto.
func TestPollVoteCurrentAndNewVersions(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	poll := &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Yes")}}}
	vote := func(target string) *waE2E.Message {
		return &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
			PollCreationMessageKey: &waCommon.MessageKey{ID: proto.String(target)},
			Vote:                   &waE2E.PollEncValue{EncPayload: []byte("cipher"), EncIV: []byte("iv")},
			Metadata:               &waE2E.PollUpdateMessageMetadata{PollNameHash: []byte("h")},
			SenderTimestampMS:      proto.Int64(10500),
		}}
	}
	p.Handle(messageEvent("g@g.us", "P1", &waE2E.Message{PollCreationMessage: poll}))
	p.Handle(messageEvent("g@g.us", "P5", &waE2E.Message{PollCreationMessageV5: poll}))
	p.Handle(messageEvent("g@g.us", "V1", vote("P1")))
	p.Handle(messageEvent("g@g.us", "V5", vote("P5")))
	if got := rows(t, st, `SELECT id FROM messages ORDER BY id`); !slices.Equal(got, []string{"P1", "P5"}) {
		t.Fatalf("messages=%v", got)
	}
	if got := rows(t, st, `SELECT kind,target_id,sender_jid,ts,length(raw_proto)>0 FROM message_events ORDER BY target_id`); !slices.Equal(got, []string{"poll_vote|P1|2@lid|10|1", "poll_vote|P5|2@lid|10|1"}) {
		t.Fatalf("message_events=%v", got)
	}
}

// M6: protocol messages that carry key material (app-state sync keys, contact hash keys, the media key of a history
// blob or of a peer-data response) are stored without their raw proto, so backups of archive.db never hold keys.
func TestKeyShareProtocolMessageKeepsNoRawProto(t *testing.T) {
	keyShare := protocolMsg(waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum(), "")
	keyShare.AppStateSyncKeyShare = &waE2E.AppStateSyncKeyShare{Keys: []*waE2E.AppStateSyncKey{{KeyData: &waE2E.AppStateSyncKeyData{KeyData: []byte("secret key")}}}}
	history := protocolMsg(waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(), "")
	history.HistorySyncNotification = &waE2E.HistorySyncNotification{MediaKey: []byte("media key"), DirectPath: proto.String("/v/t62")}
	hashKey := protocolMsg(waE2E.ProtocolMessage_SHARED_DEVICE_CONTACT_HASH_KEY_SHARE.Enum(), "")
	hashKey.SharedDeviceContactHashKeyShare = &waE2E.SharedDeviceContactHashKeyShare{Keys: []*waE2E.SharedDeviceContactHashKey{{KeyData: []byte("hash key")}}}
	peer := protocolMsg(waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE.Enum(), "")
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	for i, pm := range []*waE2E.ProtocolMessage{keyShare, history, hashKey, peer} {
		p.Handle(messageEvent("g@g.us", string(rune('A'+i)), &waE2E.Message{ProtocolMessage: pm}))
	}
	want := []string{"APP_STATE_SYNC_KEY_SHARE|0", "HISTORY_SYNC_NOTIFICATION|0", "PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE|0", "SHARED_DEVICE_CONTACT_HASH_KEY_SHARE|0"}
	if got := rows(t, st, `SELECT value,length(raw_proto) FROM message_events WHERE kind='protocol' ORDER BY value`); !slices.Equal(got, want) {
		t.Fatalf("message_events=%v", got)
	}
	if got := rows(t, st, `SELECT count(*) FROM messages`); !slices.Equal(got, []string{"0"}) {
		t.Fatalf("messages=%v", got)
	}
}

// I3: an edit encrypted with the message secret (SecretEncryptedMessage, MESSAGE_EDIT) is an edit of its target;
// the new content is encrypted, so NewText stays empty and the raw proto is kept, like a poll vote.
func TestSecretEncryptedEditIsAnEdit(t *testing.T) {
	msg := &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
		TargetMessageKey: &waCommon.MessageKey{ID: proto.String("ORIG")}, EncPayload: []byte("cipher"), EncIV: []byte("iv"),
		SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum(),
	}}
	r := ingest.FromMessage(messageEvent("g@g.us", "SE", msg), "live", time.Unix(20, 0))
	if r.Kind != ingest.KindEdit || r.Message != nil || r.Event == nil {
		t.Fatalf("kind=%s message=%+v event=%+v", r.Kind, r.Message, r.Event)
	}
	if e := r.Event; e.Kind != "edit" || e.TargetID != "ORIG" || e.NewText != "" || e.Timestamp != 10 || len(e.RawProto) == 0 {
		t.Fatalf("event=%+v", e)
	}
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	p.Handle(messageEvent("g@g.us", "SE", msg))
	if got := rows(t, st, `SELECT count(*) FROM messages`); !slices.Equal(got, []string{"0"}) {
		t.Fatalf("messages=%v", got)
	}
	if got := rows(t, st, `SELECT kind,target_id,new_text,length(raw_proto)>0 FROM message_events`); !slices.Equal(got, []string{"edit|ORIG||1"}) {
		t.Fatalf("message_events=%v", got)
	}
}

// M3: an edited image, video or document caption is the edit's new text.
func TestEditedMediaCaptionIsNewText(t *testing.T) {
	for _, content := range []*waE2E.Message{
		{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("new caption")}},
		{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("new caption")}},
		{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("new caption")}},
	} {
		pm := protocolMsg(waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), "ORIG")
		pm.EditedMessage = content
		r := ingest.FromMessage(messageEvent("g@g.us", "EC", &waE2E.Message{ProtocolMessage: pm}), "live", time.Unix(20, 0))
		if r.Kind != ingest.KindEdit || r.Event == nil || r.Event.NewText != "new caption" || r.Media != nil {
			t.Fatalf("content=%v kind=%s event=%+v media=%+v", content, r.Kind, r.Event, r.Media)
		}
	}
}
