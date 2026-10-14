package ingest_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
)

// Real-looking identifiers; none of them may survive Scrub.
const (
	phoneA = "4915123456789"
	phoneB = "4915999888777"
	lidA   = "123456789012345"
	groupA = "120363011111111111"
)

// scrubbedFiles returns each output file's text and its decoded protobuf bytes.
func scrubbedFiles(t *testing.T, dir string) (texts, protos []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(data))
		var rec struct {
			Proto string `json:"proto"`
		}
		if err = json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		raw, err := base64.StdEncoding.DecodeString(rec.Proto)
		if err != nil {
			t.Fatal(err)
		}
		protos = append(protos, string(raw))
	}
	return texts, protos
}

func scrub(t *testing.T, in string) string {
	t.Helper()
	out := t.TempDir()
	if err := ingest.Scrub(in, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// H14 (a): numbers inside the protobuf (participant, mentions, history mappings and conversation fields) are replaced,
// key bytes are cleared, text is kept, the same number maps to the same fake everywhere, and the output is stable.
func TestScrubRemovesNumbersInsideProtobuf(t *testing.T) {
	in := t.TempDir()
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("see you"), ContextInfo: &waE2E.ContextInfo{
			Participant: proto.String(phoneA + "@s.whatsapp.net"), MentionedJID: []string{phoneB + "@s.whatsapp.net"}, RemoteJID: proto.String(groupA + "@g.us")}},
		ImageMessage: &waE2E.ImageMessage{Caption: proto.String("caption kept"), MediaKey: []byte("secret-media-key")},
	}
	e := messageEvent(groupA+"@g.us", "A", msg)
	e.Info.Sender = jid(phoneA + ":12@s.whatsapp.net")
	hs := &waHistorySync.HistorySync{
		PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{{PnJID: proto.String(phoneA + "@s.whatsapp.net"), LidJID: proto.String(lidA + "@lid")}},
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(lidA + "@lid"), AuthAgentObaPhoneNumber: proto.String(phoneB), Messages: []*waHistorySync.HistorySyncMsg{{
			Message: &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{RemoteJID: proto.String(lidA + "@lid"), Participant: proto.String(phoneA + ":3@s.whatsapp.net"), ID: proto.String("H")}, Message: text("history text")}}}}},
	}
	rec := ingest.NewRecorder(in).Wrap(func(any) {})
	rec(e)
	rec(&events.HistorySync{Data: hs})
	rec(&events.Archive{JID: jid(groupA + "@g.us"), Timestamp: time.Unix(5, 0), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}})
	out := scrub(t, in)
	texts, protos := scrubbedFiles(t, out)
	all := strings.Join(texts, "\n") + strings.Join(protos, "\n")
	for _, original := range []string{phoneA, phoneB, lidA, groupA, "secret-media-key"} {
		if strings.Contains(all, original) {
			t.Errorf("%s survives Scrub", original)
		}
	}
	for _, kept := range []string{"see you", "caption kept", "history text"} {
		if !strings.Contains(all, kept) {
			t.Errorf("text %q was not kept", kept)
		}
	}
	loaded, err := ingest.Load(out)
	if err != nil || len(loaded) != 3 {
		t.Fatalf("scrubbed output loads %d events, err %v", len(loaded), err)
	}
	m := loaded[0].(*events.Message)
	fakeA := m.Info.Sender.User
	ctx := m.Message.GetExtendedTextMessage().GetContextInfo()
	h := loaded[1].(*events.HistorySync).Data
	if fakeA == phoneA || ctx.GetParticipant() != fakeA+"@s.whatsapp.net" || h.GetPhoneNumberToLidMappings()[0].GetPnJID() != fakeA+"@s.whatsapp.net" ||
		h.GetConversations()[0].GetMessages()[0].GetMessage().GetKey().GetParticipant() != fakeA+":3@s.whatsapp.net" {
		t.Errorf("phone %s not mapped consistently: sender %s, participant %s, mapping %s", phoneA, m.Info.Sender, ctx.GetParticipant(), h.GetPhoneNumberToLidMappings()[0].GetPnJID())
	}
	fakeLID := h.GetPhoneNumberToLidMappings()[0].GetLidJID()
	if h.GetConversations()[0].GetID() != fakeLID || !strings.HasSuffix(fakeLID, "@lid") {
		t.Errorf("LID mapped inconsistently: %s vs %s", h.GetConversations()[0].GetID(), fakeLID)
	}
	if bare := h.GetConversations()[0].GetAuthAgentObaPhoneNumber(); bare+"@s.whatsapp.net" != ctx.GetMentionedJID()[0] {
		t.Errorf("bare number %s and mention %s of the same phone differ", bare, ctx.GetMentionedJID()[0])
	}
	if m.Info.Chat.String() != loaded[2].(*events.Archive).JID.String() || m.Info.Chat.Server != types.GroupServer {
		t.Errorf("group %s mapped inconsistently: %s vs %s", groupA, m.Info.Chat, loaded[2].(*events.Archive).JID)
	}
	if len(m.Message.GetImageMessage().GetMediaKey()) != 0 {
		t.Error("media key kept")
	}
	again := scrub(t, in)
	texts2, _ := scrubbedFiles(t, again)
	if !bytes.Equal([]byte(strings.Join(texts, "\n")), []byte(strings.Join(texts2, "\n"))) {
		t.Error("two Scrub runs differ")
	}
}

// H14 (a): device- and agent-suffixed JIDs, the normal real-world shape, are replaced; the suffix and server stay.
func TestScrubDeviceSuffixedJID(t *testing.T) {
	in := t.TempDir()
	e := messageEvent(phoneA+"-1600000000@g.us", "A", text("hi"))
	e.Info.Sender = jid(phoneA + ":12@s.whatsapp.net")
	e.Info.SenderAlt = jid(lidA + ":3@lid")
	e.Info.RecipientAlt = jid(phoneA + "@s.whatsapp.net")
	e.Info.BroadcastListOwner = jid(phoneB + ".1:7@hosted")
	ingest.NewRecorder(in).Wrap(func(any) {})(e)
	out := scrub(t, in)
	texts, _ := scrubbedFiles(t, out)
	for _, original := range []string{phoneA, phoneB, lidA} {
		if strings.Contains(texts[0], original) {
			t.Errorf("%s survives Scrub:\n%s", original, texts[0])
		}
	}
	loaded, err := ingest.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	info := loaded[0].(*events.Message).Info
	s, alt, owner := info.Sender, info.SenderAlt, info.BroadcastListOwner
	if s.Device != 12 || s.Server != types.DefaultUserServer || s.User != info.RecipientAlt.User || s.User == phoneA {
		t.Errorf("sender %s / recipient alt %s", s, info.RecipientAlt)
	}
	if alt.Device != 3 || alt.Server != types.HiddenUserServer || alt.User == lidA {
		t.Errorf("sender alt %s", alt)
	}
	if owner.RawAgent != 1 || owner.Device != 7 || owner.Server != types.HostedServer || owner.User == phoneB {
		t.Errorf("agent-suffixed owner %s", owner)
	}
	if info.Chat.Server != types.GroupServer {
		t.Errorf("group chat %s", info.Chat)
	}
}

// H8 M6 deferral: key shares, sender keys and message secrets are cleared from fixtures; key IDs and text stay.
func TestScrubClearsKeyMaterial(t *testing.T) {
	in := t.TempDir()
	msg := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum(), AppStateSyncKeyShare: &waE2E.AppStateSyncKeyShare{Keys: []*waE2E.AppStateSyncKey{{
			KeyID: &waE2E.AppStateSyncKeyId{KeyID: []byte("key-id-kept")}, KeyData: &waE2E.AppStateSyncKeyData{KeyData: []byte("app-state-key-bytes")}}}}},
		SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{GroupID: proto.String(groupA + "@g.us"), AxolotlSenderKeyDistributionMessage: []byte("sender-key-bytes")},
		MessageContextInfo:           &waE2E.MessageContextInfo{MessageSecret: []byte("message-secret-bytes")},
		Conversation:                 proto.String("text kept"),
	}
	ingest.NewRecorder(in).Wrap(func(any) {})(messageEvent(groupA+"@g.us", "K", msg))
	_, protos := scrubbedFiles(t, scrub(t, in))
	for _, secret := range []string{"app-state-key-bytes", "sender-key-bytes", "message-secret-bytes", groupA} {
		if strings.Contains(protos[0], secret) {
			t.Errorf("%s survives Scrub", secret)
		}
	}
	for _, kept := range []string{"key-id-kept", "text kept"} {
		if !strings.Contains(protos[0], kept) {
			t.Errorf("%s was removed", kept)
		}
	}
}
