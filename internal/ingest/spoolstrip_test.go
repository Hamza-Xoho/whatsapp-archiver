package ingest_test

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"io"
	"slices"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// I3 (Hamza, option b, 26 Oct 2026): the history spool is in the backed-up archive.db, so the decrypted blob is stripped
// of message secrets and other key material before it is spooled. These tests pin the strip function (W5 calls it
// before SpoolReceive).

// secretFixture builds one synthetic HistorySync chunk. With secrets false it builds the same chunk as the strip must
// leave behind: every container stays, every secret is absent. mediaKey is not a secret here: the media worker needs a
// message's media key, and messages.raw_proto stores it already.
type secretFixture struct {
	withSecrets bool
	secrets     [][]byte // every secret planted, each a distinct 32-byte pattern
}

func (f *secretFixture) secret(n byte) []byte {
	if !f.withSecrets {
		return nil
	}
	s := bytes.Repeat([]byte{0xA0 + n}, 32)
	f.secrets = append(f.secrets, s)
	return s
}

var mediaKeyOfImage = bytes.Repeat([]byte{0x5E}, 32)

func (f *secretFixture) context(n byte) *waE2E.MessageContextInfo {
	return &waE2E.MessageContextInfo{MessageSecret: f.secret(n), BotMessageSecret: f.secret(n + 1), DeviceListMetadataVersion: proto.Int32(2)}
}

func (f *secretFixture) message(id string, content *waE2E.Message, n byte) *waWeb.WebMessageInfo {
	m := webMsg(id, "3@lid", false, content)
	m.MessageSecret = f.secret(n)
	m.ReportingTokenInfo = &waWeb.ReportingTokenInfo{ReportingTag: f.secret(n + 1), ReportingTagTimestamp: proto.Uint64(77)}
	return m
}

func (f *secretFixture) build() *waHistorySync.HistorySync {
	target := &waCommon.MessageKey{ID: proto.String("TEXT"), RemoteJID: proto.String("g@g.us")}
	text1 := &waE2E.Message{Conversation: proto.String("hello"), MessageContextInfo: f.context(1)}
	quoted := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("replying"), ContextInfo: &waE2E.ContextInfo{
		StanzaID: proto.String("TEXT"), QuotedMessage: &waE2E.Message{Conversation: proto.String("hello"), MessageContextInfo: f.context(3)}}}, MessageContextInfo: f.context(5)}
	edit := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{Key: target, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			EditedMessage: &waE2E.Message{Conversation: proto.String("hello, edited"), MessageContextInfo: f.context(7)}},
		MessageContextInfo: f.context(9)}
	wrappedEdit := &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Key: target, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: &waE2E.Message{Conversation: proto.String("again"), MessageContextInfo: f.context(11)}},
		MessageContextInfo: f.context(13)}}}
	reaction := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: target, Text: proto.String("+1")}, MessageContextInfo: f.context(15)}
	image := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), MediaKey: mediaKeyOfImage, FileEncSHA256: []byte("enc-sha"), DirectPath: proto.String("/v/t62/x")}, MessageContextInfo: f.context(17)}
	poll := &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{EncKey: f.secret(19), Name: proto.String("lunch?"),
		Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("pizza")}}}, MessageContextInfo: f.context(20)}
	notification := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum()}}
	keyShare := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE.Enum()}}
	if f.withSecrets {
		notification.ProtocolMessage.HistorySyncNotification = &waE2E.HistorySyncNotification{MediaKey: f.secret(22), FileEncSHA256: []byte("blob-enc-sha"), DirectPath: proto.String("/blob")}
		keyShare.ProtocolMessage.AppStateSyncKeyShare = &waE2E.AppStateSyncKeyShare{Keys: []*waE2E.AppStateSyncKey{{KeyData: &waE2E.AppStateSyncKeyData{KeyData: f.secret(23)}}}}
	}

	// Secrets under the wrappers: ephemeral, view-once (v1 and v2), device-sent, document with caption, and a quoted
	// message inside an edit.
	inner := func(n byte) *waE2E.Message {
		return &waE2E.Message{Conversation: proto.String("inner"), MessageContextInfo: f.context(n)}
	}
	ephemeral := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: inner(56)}}
	viewOnce := &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: inner(58)}, ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: inner(60)}}
	deviceSent := &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{DestinationJID: proto.String("g@g.us"), Message: inner(62)}, MessageContextInfo: f.context(64)}
	docCaption := &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("syllabus"), MediaKey: mediaKeyOfImage}, MessageContextInfo: f.context(66)}}}
	editWithQuote := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: target, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("edited with a quote"),
			ContextInfo: &waE2E.ContextInfo{QuotedMessage: inner(68)}}, MessageContextInfo: f.context(70)}}}

	msgText := f.message("TEXT", text1, 24)
	msgText.Reactions = []*waWeb.Reaction{{Key: &waCommon.MessageKey{ID: proto.String("RX"), Participant: proto.String("4@lid")}, Text: proto.String("+1"), SenderTimestampMS: proto.Int64(1234)}}
	msgPoll := f.message("POLL", poll, 26)
	msgPoll.PollUpdates = []*waWeb.PollUpdate{{PollUpdateMessageKey: &waCommon.MessageKey{ID: proto.String("VOTE")}, Vote: &waE2E.PollVoteMessage{SelectedOptions: [][]byte{[]byte("hash")}}, SenderTimestampMS: proto.Int64(1235)}}
	items := func(msgs ...*waWeb.WebMessageInfo) []*waHistorySync.HistorySyncMsg {
		out := make([]*waHistorySync.HistorySyncMsg, len(msgs))
		for i, m := range msgs {
			out[i] = &waHistorySync.HistorySyncMsg{Message: m}
		}
		return out
	}
	return &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_FULL.Enum(), Progress: proto.Uint32(40), ChunkOrder: proto.Uint32(2),
		ThreadIDUserSecret: f.secret(28), ShareableChatIdentifierEncryptionKey: f.secret(29), NctSalt: f.secret(30),
		Conversations: []*waHistorySync.Conversation{
			{ID: proto.String("g@g.us"), TcToken: f.secret(31), ContactPrimaryIdentityKey: f.secret(32), Name: proto.String("Group"),
				Messages: items(msgText, f.message("QUOTE", quoted, 33), f.message("EDIT", edit, 35), f.message("EDIT2", wrappedEdit, 37),
					f.message("RX", reaction, 39), f.message("IMG", image, 41), msgPoll, f.message("NOTE", notification, 45), f.message("KEYS", keyShare, 47),
					f.message("EPH", ephemeral, 72), f.message("VO", viewOnce, 74), f.message("DEV", deviceSent, 76), f.message("DOC", docCaption, 78), f.message("EDITQ", editWithQuote, 80))},
			{ID: proto.String("5@lid"), TcToken: f.secret(49), Messages: items(f.message("DM", text1, 50))},
		},
		StatusV3Messages: []*waWeb.WebMessageInfo{f.message("STATUS", text1, 52)},
		RecentStickers:   []*waHistorySync.StickerMetadata{{URL: proto.String("https://sticker"), MediaKey: f.secret(54), DirectPath: proto.String("/s")}},
	}
}

func marshal(t *testing.T, hs *waHistorySync.HistorySync) []byte {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(hs)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func encode(t *testing.T, raw []byte, format string) []byte {
	t.Helper()
	if format == store.BlobRaw {
		return raw
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil || zw.Close() != nil {
		t.Fatal("compress", err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, blob []byte, format string) *waHistorySync.HistorySync {
	t.Helper()
	raw := blob
	if format == store.BlobZlib {
		zr, err := zlib.NewReader(bytes.NewReader(blob))
		if err != nil {
			t.Fatalf("output is not zlib: %v", err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			t.Fatalf("output is not zlib: %v", err)
		}
	}
	hs := &waHistorySync.HistorySync{}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(raw, hs); err != nil {
		t.Fatalf("output is not a HistorySync: %v", err)
	}
	return hs
}

// secretFieldNames is the test's own list of bytes fields that must be empty in the output, apart from the production
// rule: a protobuf walk over the output must find none of them.
var secretFieldNames = map[string]bool{
	"messageSecret": true, "botMessageSecret": true, "threadIDUserSecret": true, "shareableChatIdentifierEncryptionKey": true,
	"nctSalt": true, "tcToken": true, "contactPrimaryIdentityKey": true, "reportingTag": true, "encKey": true, "keyData": true,
}

func walkSecrets(m protoreflect.Message, path string, found *[]string) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		here := path + "." + string(fd.Name())
		switch {
		case fd.Kind() == protoreflect.BytesKind && !fd.IsList() && !fd.IsMap():
			if secretFieldNames[string(fd.Name())] && len(v.Bytes()) > 0 {
				*found = append(*found, here)
			}
		case fd.IsMap(), fd.Kind() != protoreflect.MessageKind:
		case fd.IsList():
			for i := 0; i < v.List().Len(); i++ {
				walkSecrets(v.List().Get(i).Message(), here, found)
			}
		default:
			walkSecrets(v.Message(), here, found)
		}
		return true
	})
}

func formats() []string { return []string{store.BlobZlib, store.BlobRaw} }

func TestStripHistorySecretsClearsEveryMessage(t *testing.T) {
	for _, format := range formats() {
		t.Run(format, func(t *testing.T) {
			with, without := &secretFixture{withSecrets: true}, &secretFixture{}
			input := with.build()
			if len(with.secrets) < 30 {
				t.Fatalf("fixture plants %d secrets, want at least 30", len(with.secrets))
			}
			out, err := ingest.StripHistorySecrets(encode(t, marshal(t, input), format), format)
			if err != nil {
				t.Fatal(err)
			}
			got := decode(t, out, format)
			raw := marshal(t, got)
			var left []int
			for i, s := range with.secrets {
				if bytes.Contains(raw, s) { // raw is the decoded output, so a zlib blob is checked after decompression
					left = append(left, i)
				}
			}
			if len(left) > 0 {
				t.Errorf("%d of %d planted secrets are still in the output: #%v", len(left), len(with.secrets), left)
			}
			var found []string
			walkSecrets(got.ProtoReflect(), "HistorySync", &found)
			if len(found) > 0 {
				t.Errorf("the output still has secret fields: %s", strings.Join(found, ", "))
			}
			if want := without.build(); !proto.Equal(want, got) {
				t.Errorf("the output is not the input minus the cleared fields (%d bytes against %d)", proto.Size(got), proto.Size(want))
			}
		})
	}
}

// A message's own media key is not stripped: the media worker needs it and messages.raw_proto already holds it. Only
// the key material of the notification and of key-share protocol messages goes.
func TestStripHistorySecretsKeepsTheMediaKeyOfAMessage(t *testing.T) {
	f := &secretFixture{withSecrets: true}
	out, err := ingest.StripHistorySecrets(encode(t, marshal(t, f.build()), store.BlobRaw), store.BlobRaw)
	if err != nil {
		t.Fatal(err)
	}
	var img *waE2E.ImageMessage
	for _, m := range decode(t, out, store.BlobRaw).GetConversations()[0].GetMessages() {
		if m.GetMessage().GetKey().GetID() == "IMG" {
			img = m.GetMessage().GetMessage().GetImageMessage()
		}
	}
	if img == nil || !bytes.Equal(img.GetMediaKey(), mediaKeyOfImage) || string(img.GetFileEncSHA256()) != "enc-sha" || img.GetDirectPath() != "/v/t62/x" {
		t.Fatalf("image message after the strip = %v, want its media key, hash and path intact", img)
	}
}

func TestStripHistorySecretsIsIdempotent(t *testing.T) {
	for _, format := range formats() {
		t.Run(format, func(t *testing.T) {
			f := &secretFixture{withSecrets: true}
			once, err := ingest.StripHistorySecrets(encode(t, marshal(t, f.build()), format), format)
			if err != nil {
				t.Fatal(err)
			}
			twice, err := ingest.StripHistorySecrets(once, format)
			if err != nil || !bytes.Equal(once, twice) {
				t.Fatalf("a second strip changed the blob (err=%v, %d bytes, then %d)", err, len(once), len(twice))
			}
			clean := &secretFixture{}
			already, err := ingest.StripHistorySecrets(encode(t, marshal(t, clean.build()), format), format)
			if err != nil || !bytes.Equal(already, once) {
				t.Fatalf("stripping a blob that never had secrets gives a different blob (err=%v)", err)
			}
		})
	}
}

func TestStripHistorySecretsDoesNotChangeItsInput(t *testing.T) {
	f := &secretFixture{withSecrets: true}
	in := encode(t, marshal(t, f.build()), store.BlobRaw)
	before := bytes.Clone(in)
	if _, err := ingest.StripHistorySecrets(in, store.BlobRaw); err != nil || !bytes.Equal(in, before) {
		t.Fatalf("input changed or failed: %v", err)
	}
}

func TestStripHistorySecretsRejectsABlobItCannotRead(t *testing.T) {
	good := encode(t, marshal(t, (&secretFixture{}).build()), store.BlobZlib)
	for name, c := range map[string]struct {
		blob   []byte
		format string
	}{
		"unknown format":    {good, "zstd"},
		"empty format":      {good, ""},
		"not zlib":          {[]byte("plainly not compressed"), store.BlobZlib},
		"truncated zlib":    {good[:len(good)/2], store.BlobZlib},
		"empty zlib":        {nil, store.BlobZlib},
		"not a protobuf":    {[]byte{0xff, 0xff, 0xff, 0xff}, store.BlobRaw},
		"truncated message": {[]byte{0x12, 0x20, 0x01}, store.BlobRaw},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := ingest.StripHistorySecrets(c.blob, c.format); err == nil {
				t.Fatalf("StripHistorySecrets succeeded with %d bytes; a blob it cannot clean must never be spooled", len(out))
			}
		})
	}
}

// An empty chunk is a valid HistorySync: it comes back valid and still in its format.
func TestStripHistorySecretsAcceptsAnEmptyChunk(t *testing.T) {
	for _, format := range formats() {
		out, err := ingest.StripHistorySecrets(encode(t, nil, format), format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if got := decode(t, out, format); len(got.GetConversations()) != 0 {
			t.Fatalf("%s: %v", format, got)
		}
	}
}

func TestStripHistorySyncReturnsACleanCopyAndKeepsTheOriginal(t *testing.T) {
	f := &secretFixture{withSecrets: true}
	in := f.build()
	original := proto.Clone(in)
	got := ingest.StripHistorySync(in)
	if !proto.Equal(in, original) {
		t.Fatal("StripHistorySync changed its argument")
	}
	if want := (&secretFixture{}).build(); !proto.Equal(want, got) {
		t.Fatalf("copy is not the expected clean chunk (%d bytes against %d)", proto.Size(got), proto.Size(want))
	}
}

// The consumer sees nothing but the secrets go: importing the stripped chunk stores the same messages and events as
// importing the original, and the archive then holds none of the planted secrets. The comparison leaves out raw_proto
// and event_id on purpose: raw_proto of the original import still holds secrets the live path does not clear (a quoted
// message's context secrets and a poll's encKey, found while writing this test), and event_id of a protocol event is
// derived from its content, which the strip changes.
func TestImportOfAStrippedChunkStoresTheSameMessages(t *testing.T) {
	f := &secretFixture{withSecrets: true}
	original := f.build()
	stripped := ingest.StripHistorySync(original)
	imported := func(hs *waHistorySync.HistorySync) (logical []string, dump string) {
		st := testkit.Store(t)
		p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
		if _, _, err := p.ImportHistory(t.Context(), &events.HistorySync{Data: hs}); err != nil {
			t.Fatal(err)
		}
		logical = append(rows(t, st, "SELECT chat_jid,id,sender_jid,kind,text,caption,quoted_id,details,ts,source FROM messages ORDER BY chat_jid,id"),
			rows(t, st, "SELECT chat_jid,target_id,kind,source_id,sender_jid,value,ts FROM message_events ORDER BY kind,source_id,target_id,value")...)
		dump, err := ingest.DumpRaw(st)
		if err != nil {
			t.Fatal(err)
		}
		return logical, dump
	}
	want, _ := imported(original)
	got, dump := imported(stripped)
	if len(want) < 10 || !slices.Equal(want, got) {
		t.Fatalf("stored rows differ (or the fixture imported too little):\nwant %q\ngot  %q", want, got)
	}
	for i, secret := range f.secrets {
		if strings.Contains(strings.ToLower(dump), strings.ToLower(hex.EncodeToString(secret))) {
			t.Errorf("planted secret #%d is in the archive after importing the stripped chunk", i)
		}
	}
}
