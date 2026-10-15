package ingest_test

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"regexp"
	"strings"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waVnameCert"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
)

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.MarshalOptions{AllowPartial: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// hidden reports whether the number is in data as is, in a zlib stream inside it, or in any base64 string inside it.
func hidden(data []byte, number string) bool {
	if bytes.Contains(data, []byte(number)) {
		return true
	}
	for i := range data { // a zlib stream may start anywhere inside a protobuf
		if data[i] != 0x78 {
			continue
		}
		if r, err := zlib.NewReader(bytes.NewReader(data[i:])); err == nil {
			if plain, _ := io.ReadAll(r); bytes.Contains(plain, []byte(number)) {
				return true
			}
		}
	}
	for _, s := range regexp.MustCompile(`[A-Za-z0-9+/]{8,}={0,2}`).FindAll(data, -1) {
		if decoded, err := base64.StdEncoding.DecodeString(string(s)); err == nil && len(decoded) > 0 && hidden(decoded, number) {
			return true
		}
	}
	return false
}

// Fix round 1, I1: serialized or compressed payloads (inline history bootstrap, placeholder resend, LID migration
// mappings), unknown group change nodes and verified-name certificates carry identifiers; Scrub clears them.
func TestScrubClearsPayloadBytes(t *testing.T) {
	in := t.TempDir()
	var inline bytes.Buffer
	zw := zlib.NewWriter(&inline)
	_, _ = zw.Write(mustMarshal(t, &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{{ID: proto.String(phoneA + "@s.whatsapp.net")}}}))
	_ = zw.Close()
	resent := mustMarshal(t, &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{RemoteJID: proto.String(phoneA + "@s.whatsapp.net"), ID: proto.String("R")}, Message: text("resent text")})
	rec := ingest.NewRecorder(in).Wrap(func(any) {})
	rec(messageEvent("1@s.whatsapp.net", "N", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
		HistorySyncNotification: &waE2E.HistorySyncNotification{InitialHistBootstrapInlinePayload: inline.Bytes()}}}))
	rec(messageEvent("1@s.whatsapp.net", "P", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE.Enum(),
		PeerDataOperationRequestResponseMessage: &waE2E.PeerDataOperationRequestResponseMessage{PeerDataOperationResult: []*waE2E.PeerDataOperationRequestResponseMessage_PeerDataOperationResult{{
			PlaceholderMessageResendResponse: &waE2E.PeerDataOperationRequestResponseMessage_PeerDataOperationResult_PlaceholderMessageResendResponse{WebMessageInfoBytes: resent}}}}}}))
	rec(messageEvent("1@s.whatsapp.net", "L", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		LidMigrationMappingSyncMessage: &waE2E.LIDMigrationMappingSyncMessage{EncodedMappingPayload: []byte("pn " + phoneA + " lid " + lidA)}}}))
	withCert := messageEvent("1@s.whatsapp.net", "V", text("from a business"))
	withCert.Info.VerifiedName = &types.VerifiedName{Certificate: &waVnameCert.VerifiedNameCertificate{Details: []byte("cert for " + phoneA), Signature: []byte(phoneB)}}
	rec(withCert)
	rec(&events.GroupInfo{JID: jid("1@g.us"), UnknownChanges: []*waBinary.Node{{Tag: "future", Content: []byte("added " + phoneA)}}})
	out := scrub(t, in)
	texts, protos := scrubbedFiles(t, out)
	for i := range texts {
		for _, number := range []string{phoneA, phoneB, lidA} {
			if hidden([]byte(texts[i]), number) || hidden([]byte(protos[i]), number) {
				t.Errorf("file %d keeps %s", i+1, number)
			}
		}
	}
	if !strings.Contains(strings.Join(texts, ""), "from a business") && !strings.Contains(strings.Join(protos, ""), "from a business") {
		t.Error("text was not kept")
	}
	if _, err := ingest.Load(out); err != nil {
		t.Fatalf("scrubbed output does not load: %v", err)
	}
}

var mentionText = regexp.MustCompile(`^hi @(\d+) and @(\d+) call$`)

// Fix round 1, I2: a mention in the text, a vCard waid and a vCard TEL value get the same fake as the JID of
// that number, and the original appears nowhere.
func TestScrubRewritesMentionsAndVCards(t *testing.T) {
	const number, lid = "10000000001", "123456789012345"
	in := t.TempDir()
	rec := ingest.NewRecorder(in).Wrap(func(any) {})
	rec(messageEvent("1@g.us", "M", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("hi @" + number + " and @" + lid + " call"),
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{number + "@s.whatsapp.net", lid + "@lid"}}}}))
	vcard := "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nTEL;type=CELL;type=VOICE;waid=" + phoneA + ":+49 151 23456789\nEND:VCARD"
	rec(messageEvent("1@g.us", "C", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Alice"), Vcard: proto.String(vcard)},
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{ContextInfo: &waE2E.ContextInfo{Participant: proto.String(phoneA + "@s.whatsapp.net")}}}))
	out := scrub(t, in)
	texts, protos := scrubbedFiles(t, out)
	all := strings.Join(texts, "") + strings.Join(protos, "")
	for _, original := range []string{number, lid, phoneA, "151 23456789"} {
		if strings.Contains(all, original) {
			t.Errorf("%s survives Scrub", original)
		}
	}
	loaded, err := ingest.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	x := loaded[0].(*events.Message).Message.GetExtendedTextMessage()
	m := mentionText.FindStringSubmatch(x.GetText())
	if m == nil {
		t.Fatalf("text %q lost its shape", x.GetText())
	}
	if mentions := x.GetContextInfo().GetMentionedJID(); mentions[0] != m[1]+"@s.whatsapp.net" || mentions[1] != m[2]+"@lid" {
		t.Errorf("text mentions %s, %s but mentionedJID is %v", m[1], m[2], mentions)
	}
	c := loaded[1].(*events.Message).Message
	fake := strings.TrimSuffix(c.GetExtendedTextMessage().GetContextInfo().GetParticipant(), "@s.whatsapp.net")
	want := "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nTEL;type=CELL;type=VOICE;waid=" + fake + ":+" + fake + "\nEND:VCARD"
	if got := c.GetContactMessage().GetVcard(); got != want {
		t.Errorf("vcard:\n%s\nwant:\n%s", got, want)
	}
}

// H14 final wave: an app-state snapshot in a fatal-recovery response (zlib-compressed, carrying JIDs) is cleared.
func TestScrubClearsCollectionSnapshot(t *testing.T) {
	in := t.TempDir()
	var snapshot bytes.Buffer
	zw := zlib.NewWriter(&snapshot)
	_, _ = zw.Write([]byte(`["mute","` + phoneA + `@s.whatsapp.net"]`))
	_ = zw.Close()
	rec := ingest.NewRecorder(in).Wrap(func(any) {})
	rec(messageEvent("1@s.whatsapp.net", "S", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_RESPONSE_MESSAGE.Enum(),
		PeerDataOperationRequestResponseMessage: &waE2E.PeerDataOperationRequestResponseMessage{PeerDataOperationResult: []*waE2E.PeerDataOperationRequestResponseMessage_PeerDataOperationResult{{
			SyncdSnapshotFatalRecoveryResponse: &waE2E.PeerDataOperationRequestResponseMessage_PeerDataOperationResult_SyncDSnapshotFatalRecoveryResponse{
				CollectionSnapshot: snapshot.Bytes(), IsCompressed: proto.Bool(true)}}}}}}))
	texts, protos := scrubbedFiles(t, scrub(t, in))
	if len(texts) != 1 {
		t.Fatalf("%d scrubbed files, want 1", len(texts))
	}
	if hidden([]byte(texts[0]), phoneA) || hidden([]byte(protos[0]), phoneA) {
		t.Errorf("the snapshot keeps %s", phoneA)
	}
}
