package ingest_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// W2(c): one golden dump per message kind, plus one for the event shapes. Each scenario is generated here, recorded to
// testdata/events/<name>, replayed through the pipeline by TestReplayMatchesGolden and compared with
// testdata/golden/<name>.sql. -update regenerates the events and, in TestReplayMatchesGolden, the dumps: read every
// line of the dumps and log the review in docs/v1/STATE.md first (AGENTS.md).

// kindMessage is one message of the synthetic group at second sec; id is made unique per scenario by the caller.
func kindMessage(id, sender, senderPN, push string, sec int64, msg *waE2E.Message) *events.Message {
	return synMessage(synInfo(id, sender, senderPN, push, sec), msg)
}

func alice(id string, sec int64, msg *waE2E.Message) *events.Message {
	return kindMessage(id, aliceLID, alicePN, "Alice", sec, msg)
}

func bob(id string, sec int64, msg *waE2E.Message) *events.Message {
	return kindMessage(id, bobLID, bobPN, "Bob", sec, msg)
}

var smallJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0xFF, 0xD9}

func fromMe(e *events.Message, push string) *events.Message {
	e.Info.IsFromMe, e.Info.PushName = true, push
	e.Info.Sender = jid(alicePN)
	return e
}

// kindScenarios maps a scenario name to the events recorded for it, in order.
func kindScenarios() map[string][]any {
	options := func(names ...string) []*waE2E.PollCreationMessage_Option {
		var out []*waE2E.PollCreationMessage_Option
		for _, n := range names {
			out = append(out, &waE2E.PollCreationMessage_Option{OptionName: proto.String(n)})
		}
		return out
	}
	return map[string][]any{
		"kind-text": {
			alice("K-TEXT-1", 60, &waE2E.Message{Conversation: proto.String("Plain text")}),
			// Arabic with diacritics, tatweel and an RTL mark, mentioning Bob by LID and Carol by phone number.
			bob("K-TEXT-2", 120, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("السَّلامُ عَلَيْكُمْ يا @20000000001 و@10000000003 في المجمـــوعة‏"),
				ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{aliceLID, carolPN}}}}),
			alice("K-TEXT-3", 180, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("A reply"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("K-TEXT-2"),
				Participant: proto.String(bobLID), QuotedMessage: &waE2E.Message{Conversation: proto.String("quoted body is not stored apart")}}}}),
			bob("K-TEXT-4", 240, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("Forwarded many times"), ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(5)}}}),
			alice("K-TEXT-5", 300, &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("This will vanish"),
				ContextInfo: &waE2E.ContextInfo{Expiration: proto.Uint32(604800)}}}}}),
			bob("K-TEXT-6", 360, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("look https://example.org/page"), MatchedText: proto.String("https://example.org/page"),
				Title: proto.String("An example page"), Description: proto.String("Nothing to see")}}),
			fromMe(kindMessage("K-TEXT-7", aliceLID, alicePN, "Alice", 420, &waE2E.Message{Conversation: proto.String("Sent from the archived phone")}), "Hamza"),
		},
		"kind-image": {
			alice("K-IMAGE-1", 60, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("A photo"), FileLength: proto.Uint64(2048),
				Width: proto.Uint32(640), Height: proto.Uint32(480), JPEGThumbnail: smallJPEG, DirectPath: proto.String("/v/t62.7118-24/synthetic-image")}}),
			bob("K-IMAGE-2", 120, &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"),
				ViewOnce: proto.Bool(true), FileLength: proto.Uint64(1024)}}}}),
		},
		"kind-video": {
			alice("K-VIDEO-1", 60, &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Caption: proto.String("A clip"), Seconds: proto.Uint32(31),
				Width: proto.Uint32(720), Height: proto.Uint32(1280), FileLength: proto.Uint64(900000), JPEGThumbnail: smallJPEG}}),
			bob("K-VIDEO-2", 120, &waE2E.Message{PtvMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(9), Width: proto.Uint32(480), Height: proto.Uint32(480)}}),
		},
		"kind-gif": {
			alice("K-GIF-1", 60, &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true), Seconds: proto.Uint32(3), Width: proto.Uint32(320), Height: proto.Uint32(240)}}),
		},
		"kind-audio": {
			alice("K-AUDIO-1", 60, &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/mpeg"), Seconds: proto.Uint32(184), FileLength: proto.Uint64(2900000)}}),
		},
		"kind-voice": {
			bob("K-VOICE-1", 60, &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(12), FileLength: proto.Uint64(18000)}}),
		},
		"kind-document": {
			alice("K-DOC-1", 60, &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"),
				FileName: proto.String("syllabus.pdf"), PageCount: proto.Uint32(14), FileLength: proto.Uint64(77000), Caption: proto.String("Syllabus for term 2"), JPEGThumbnail: smallJPEG}}}}),
		},
		"kind-sticker": {
			alice("K-STICKER-1", 60, &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), Width: proto.Uint32(512), Height: proto.Uint32(512)}}),
			bob("K-STICKER-2", 120, &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp"), IsAnimated: proto.Bool(true), Width: proto.Uint32(512), Height: proto.Uint32(512)}}),
		},
		"kind-location": {
			alice("K-LOC-1", 60, &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(24.7136), DegreesLongitude: proto.Float64(46.6753),
				Name: proto.String("Riyadh"), Address: proto.String("King Fahd Rd"), URL: proto.String("https://maps.example/riyadh")}}),
			bob("K-LOC-2", 120, &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(21.4858), DegreesLongitude: proto.Float64(39.1925)}}),
		},
		"kind-live-location": {
			alice("K-LIVE-1", 60, &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{DegreesLatitude: proto.Float64(24.5), DegreesLongitude: proto.Float64(46.5),
				AccuracyInMeters: proto.Uint32(12), SpeedInMps: proto.Float32(3.5), SequenceNumber: proto.Int64(7), Caption: proto.String("On my way")}}),
		},
		"kind-contact": {
			alice("K-CONTACT-1", 60, &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Omar Test"),
				Vcard: proto.String("BEGIN:VCARD\nVERSION:3.0\nFN:Omar Test\nTEL;type=CELL;waid=10000000009:+10000000009\nEND:VCARD")}}),
		},
		"kind-contacts": {
			bob("K-CONTACTS-1", 60, &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{DisplayName: proto.String("Omar Test and 1 other"), Contacts: []*waE2E.ContactMessage{
				{DisplayName: proto.String("Omar Test"), Vcard: proto.String("BEGIN:VCARD\nFN:Omar Test\nEND:VCARD")},
				{DisplayName: proto.String("Layla Test"), Vcard: proto.String("BEGIN:VCARD\nFN:Layla Test\nEND:VCARD")}}}}),
		},
		"kind-poll": {
			alice("K-POLL-1", 60, &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), SelectableOptionsCount: proto.Uint32(1), Options: options("Yes", "No", "غداً")}}),
			bob("K-POLL-2", 120, &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("Where?"), Options: options("Here", "There")}}),
		},
		"kind-event": {
			alice("K-EVENT-1", 60, &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Picnic"), Description: proto.String("Bring food"), StartTime: proto.Int64(1760003600),
				EndTime: proto.Int64(1760007200), Location: &waE2E.LocationMessage{Name: proto.String("The park"), DegreesLatitude: proto.Float64(24.5), DegreesLongitude: proto.Float64(46.5)}}}),
			bob("K-EVENT-2", 120, &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Called off"), IsCanceled: proto.Bool(true), StartTime: proto.Int64(1760010000)}}),
		},
		"kind-system": {
			alice("K-SYSTEM-1", 60, &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{}}),
			bob("K-SYSTEM-2", 120, &waE2E.Message{ScheduledCallCreationMessage: &waE2E.ScheduledCallCreationMessage{Title: proto.String("Standup")}}),
		},
		"kind-unknown": {
			alice("K-UNKNOWN-1", 60, &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{GroupName: proto.String("Another group")}}),
			bob("K-UNKNOWN-2", 120, &waE2E.Message{OrderMessage: &waE2E.OrderMessage{}, GroupInviteMessage: &waE2E.GroupInviteMessage{}}),
			alice("K-UNKNOWN-3", 180, &waE2E.Message{}),
		},
		"events-messages": {
			alice("E-TEXT-1", 60, &waE2E.Message{Conversation: proto.String("Target of the events")}),
			alice("E-POLL-1", 70, &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), Options: options("Yes", "No")}}),
			bob("E-REACT-1", 120, &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: groupKey("E-TEXT-1", aliceLID), Text: proto.String("👍"), SenderTimestampMS: proto.Int64(synAt(120).UnixMilli())}}),
			bob("E-REACT-2", 130, &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: groupKey("E-TEXT-1", aliceLID), Text: proto.String(""), SenderTimestampMS: proto.Int64(synAt(130).UnixMilli())}}),
			editEvent("E-EDIT-1", 180, "E-TEXT-1", "Target of the events, edited"),
			bob("E-PIN-1", 240, &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{Key: groupKey("E-TEXT-1", aliceLID), Type: waE2E.PinInChatMessage_PIN_FOR_ALL.Enum()}}),
			bob("E-VOTE-1", 300, &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: groupKey("E-POLL-1", aliceLID),
				Vote: &waE2E.PollEncValue{EncPayload: []byte("synthetic cipher"), EncIV: []byte("synthetic iv")}, SenderTimestampMS: proto.Int64(synAt(300).UnixMilli())}}),
			bob("E-PROTO-1", 360, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_EPHEMERAL_SETTING.Enum(), EphemeralExpiration: proto.Uint32(86400)}}),
			bob("E-SECRET-1", 420, &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{TargetMessageKey: groupKey("E-POLL-1", aliceLID), SecretEncType: waE2E.SecretEncryptedMessage_POLL_EDIT.Enum(),
				EncPayload: []byte("synthetic cipher"), EncIV: []byte("synthetic iv")}}),
			revokeEvent("E-REVOKE-1", 480, "E-TEXT-1"),
		},
		"events-undecryptable": {
			&events.UndecryptableMessage{Info: synInfo("U-OTHER-1", bobLID, bobPN, "Bob", 60)},
			&events.UndecryptableMessage{Info: synInfo("U-NOKEY-1", bobLID, bobPN, "Bob", 120), IsUnavailable: true},
			&events.UndecryptableMessage{Info: synInfo("U-VIEWONCE-1", aliceLID, alicePN, "Alice", 180), IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce},
			&events.UndecryptableMessage{Info: synInfo("U-HIDDEN-1", aliceLID, alicePN, "Alice", 240), DecryptFailMode: events.DecryptFailHide},
		},
		"events-history": {
			&events.HistorySync{Data: &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_RECENT.Enum(), ChunkOrder: proto.Uint32(1), Progress: proto.Uint32(100),
				Conversations: []*waHistorySync.Conversation{{ID: proto.String(synGroup), Name: proto.String("Synthetic Group"), Messages: []*waHistorySync.HistorySyncMsg{
					{Message: historyReactions(synWeb("H-TEXT-1", aliceLID, 60, &waE2E.Message{Conversation: proto.String("History message with reactions")}))},
					{Message: historyVotes(synWeb("H-POLL-1", aliceLID, 120, &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?"), Options: options("Yes", "No", "Maybe")}}))},
					{Message: historyReactions(synStub("H-STUB-1", bobLID, 180))},
				}}}}},
		},
	}
}

func synWeb(id, participant string, sec int64, msg *waE2E.Message) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String(id), Participant: proto.String(participant)},
		MessageTimestamp: proto.Uint64(uint64(synAt(sec).Unix())), PushName: proto.String("Alice"), Message: msg}
}

func synStub(id, participant string, sec int64) *waWeb.WebMessageInfo {
	web := synWeb(id, participant, sec, nil)
	web.MessageStubType = waWeb.WebMessageInfo_CIPHERTEXT.Enum()
	return web
}

func historyReactions(web *waWeb.WebMessageInfo) *waWeb.WebMessageInfo {
	web.Reactions = []*waWeb.Reaction{
		{Key: &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String("HR-1"), Participant: proto.String(bobLID)}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(synAt(200).UnixMilli())},
		{Key: &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String("HR-2"), Participant: proto.String(aliceLID)}, Text: proto.String("❤"), SenderTimestampMS: proto.Int64(synAt(210).UnixMilli())},
	}
	return web
}

func historyVotes(web *waWeb.WebMessageInfo) *waWeb.WebMessageInfo {
	web.PollUpdates = []*waWeb.PollUpdate{
		{PollUpdateMessageKey: &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String("HV-1"), Participant: proto.String(bobLID)},
			Vote: &waE2E.PollVoteMessage{SelectedOptions: [][]byte{hashOf("No"), hashOf("Maybe")}}, SenderTimestampMS: proto.Int64(synAt(300).UnixMilli())},
		{PollUpdateMessageKey: &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String("HV-2"), Participant: proto.String(aliceLID)},
			Vote: &waE2E.PollVoteMessage{}, SenderTimestampMS: proto.Int64(synAt(310).UnixMilli())},
	}
	return web
}

func editEvent(id string, sec int64, target, body string) *events.Message {
	e := bob(id, sec, &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Key: groupKey(target, aliceLID), Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: &waE2E.Message{Conversation: proto.String(body)}}}}})
	e.Info.Edit = types.EditAttributeMessageEdit
	return e
}

func revokeEvent(id string, sec int64, target string) *events.Message {
	e := alice(id, sec, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: groupKey(target, aliceLID), Type: waE2E.ProtocolMessage_REVOKE.Enum()}})
	e.Info.Edit = types.EditAttributeSenderRevoke
	return e
}

// The committed kind scenarios are exactly what kindScenarios records; -update regenerates them.
func TestKindEventsMatchGenerator(t *testing.T) {
	for name, evts := range kindScenarios() {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "events", name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				recordInto(t, dir, evts)
			}
			fresh := t.TempDir()
			recordInto(t, fresh, evts)
			list := func(d string) []string {
				entries, err := os.ReadDir(d)
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for _, e := range entries {
					out = append(out, e.Name())
				}
				return out
			}
			want, got := list(fresh), list(dir)
			if !slices.Equal(want, got) {
				t.Fatalf("testdata files %v, generator writes %v", got, want)
			}
			for _, n := range want {
				a, _ := os.ReadFile(filepath.Join(fresh, n))
				b, _ := os.ReadFile(filepath.Join(dir, n))
				if !bytes.Equal(a, b) {
					t.Errorf("%s differs from the generator output", n)
				}
			}
		})
	}
}

// Every message kind of the store has its own golden scenario.
func TestEveryKindHasAGoldenScenario(t *testing.T) {
	scenarios := kindScenarios()
	for _, kind := range []string{"text", "image", "video", "audio", "voice", "document", "sticker", "gif", "location", "live-location", "contact", "contacts", "poll", "event", "system", "unknown"} {
		if _, ok := scenarios["kind-"+kind]; !ok {
			t.Errorf("no scenario kind-%s", kind)
		}
		if _, err := os.Stat(filepath.Join("testdata", "golden", "kind-"+kind+".sql")); err != nil && !*update {
			t.Errorf("no golden for kind-%s: %v", kind, strings.TrimSpace(err.Error()))
		}
	}
}
