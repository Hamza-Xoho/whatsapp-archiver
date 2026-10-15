package ingest_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
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
	"whatsapp-archiver/internal/testkit"
)

// -update rewrites testdata/events/synthetic (TestSyntheticEventsMatchGenerator) or testdata/golden
// (TestReplayMatchesGolden). Run it only after reviewing every line and logging the review in PROGRESS.md.
var update = flag.Bool("update", false, "rewrite generated fixtures and golden dumps")

// Obviously fake identifiers: a group, Alice, Bob, Carol (history only) and Dave (added later).
const (
	synGroup = "10000000100@g.us"
	aliceLID = "20000000001@lid"
	alicePN  = "10000000001@s.whatsapp.net"
	bobLID   = "20000000002@lid"
	bobPN    = "10000000002@s.whatsapp.net"
	carolLID = "20000000003@lid"
	carolPN  = "10000000003@s.whatsapp.net"
	daveLID  = "20000000004@lid"
)

var synBase = time.Unix(1_760_000_000, 0).UTC()

func synAt(sec int64) time.Time { return synBase.Add(time.Duration(sec) * time.Second) }

func synInfo(id, sender, senderPN, push string, sec int64) types.MessageInfo {
	return types.MessageInfo{MessageSource: types.MessageSource{Chat: jid(synGroup), Sender: jid(sender), IsGroup: true, AddressingMode: types.AddressingModeLID, SenderAlt: jid(senderPN)},
		ID: types.MessageID(id), PushName: push, Timestamp: synAt(sec), Type: "text"}
}

func synMessage(info types.MessageInfo, raw *waE2E.Message) *events.Message {
	return (&events.Message{Info: info, RawMessage: raw}).UnwrapRaw()
}

func groupKey(id, participant string) *waCommon.MessageKey {
	return &waCommon.MessageKey{RemoteJID: proto.String(synGroup), ID: proto.String(id), Participant: proto.String(participant)}
}

// syntheticEvents is the Go-constructed scenario: one event of every supported kind, in a plausible order.
func syntheticEvents() []any {
	edit := synInfo("SYN-EDIT-1", aliceLID, alicePN, "Alice", 180)
	edit.Edit = types.EditAttributeMessageEdit
	revoke := synInfo("SYN-REVOKE-1", bobLID, bobPN, "Bob", 240)
	revoke.Edit = types.EditAttributeSenderRevoke
	image := synInfo("SYN-IMAGE-1", aliceLID, alicePN, "Alice", 360)
	image.Type, image.MediaType = "media", "image"
	gap := synInfo("SYN-GAP-1", bobLID, bobPN, "Bob", 420)
	return []any{
		&events.JoinedGroup{Type: "new", Notify: "create", Sender: jidPtr(aliceLID), SenderPN: jidPtr(alicePN), GroupInfo: types.GroupInfo{
			JID: jid(synGroup), OwnerJID: jid(aliceLID), OwnerPN: jid(alicePN), GroupName: types.GroupName{Name: "Synthetic Group", NameSetAt: synAt(0), NameSetBy: jid(aliceLID)},
			AddressingMode: types.AddressingModeLID, GroupCreated: synAt(0), ParticipantCount: 2,
			Participants: []types.GroupParticipant{{JID: jid(aliceLID), PhoneNumber: jid(alicePN), LID: jid(aliceLID), IsAdmin: true, IsSuperAdmin: true}, {JID: jid(bobLID), PhoneNumber: jid(bobPN), LID: jid(bobLID)}}}},
		synMessage(synInfo("SYN-TEXT-1", aliceLID, alicePN, "Alice", 60), &waE2E.Message{Conversation: proto.String("Hello from the synthetic scenario")}),
		synMessage(synInfo("SYN-REPLY-1", bobLID, bobPN, "Bob", 120), &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("A reply to Alice"),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("SYN-TEXT-1"), Participant: proto.String(aliceLID), QuotedMessage: &waE2E.Message{Conversation: proto.String("Hello from the synthetic scenario")}}}}),
		synMessage(edit, &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
			Key: groupKey("SYN-TEXT-1", aliceLID), Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: &waE2E.Message{Conversation: proto.String("Hello, edited")}}}}}),
		synMessage(revoke, &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: groupKey("SYN-REPLY-1", bobLID), Type: waE2E.ProtocolMessage_REVOKE.Enum()}}),
		synMessage(synInfo("SYN-REACT-1", bobLID, bobPN, "Bob", 300), &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: groupKey("SYN-TEXT-1", aliceLID), Text: proto.String("👍")}}),
		synMessage(image, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("A synthetic photo"), FileLength: proto.Uint64(2048), DirectPath: proto.String("/v/t62.7118-24/synthetic")}}),
		&events.UndecryptableMessage{Info: gap},
		synMessage(gap, &waE2E.Message{Conversation: proto.String("Resent after a decryption gap")}),
		&events.HistorySync{Data: &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(), ChunkOrder: proto.Uint32(1), Progress: proto.Uint32(100),
			PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{{PnJID: proto.String(carolPN), LidJID: proto.String(carolLID)}},
			Conversations: []*waHistorySync.Conversation{{ID: proto.String(carolLID), Name: proto.String("Carol"), Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{
				Key: &waCommon.MessageKey{RemoteJID: proto.String(carolLID), ID: proto.String("SYN-HIST-1")}, MessageTimestamp: proto.Uint64(uint64(synAt(-86400).Unix())), PushName: proto.String("Carol"),
				Message: &waE2E.Message{Conversation: proto.String("An older message from history")}}}}}}}},
		&events.GroupInfo{JID: jid(synGroup), Sender: jidPtr(aliceLID), SenderPN: jidPtr(alicePN), Timestamp: synAt(600), Name: &types.GroupName{Name: "Synthetic Group Renamed", NameSetAt: synAt(600), NameSetBy: jid(aliceLID)}},
		&events.GroupInfo{JID: jid(synGroup), Sender: jidPtr(aliceLID), SenderPN: jidPtr(alicePN), Timestamp: synAt(660), Join: []types.JID{jid(daveLID)}},
		&events.Archive{JID: jid(synGroup), Timestamp: synAt(700), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}},
		&events.Archive{JID: jid(synGroup), Timestamp: synAt(760), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(false)}},
		&events.Mute{JID: jid(synGroup), Timestamp: synAt(800), Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(synAt(4400).UnixMilli())}},
		&events.Pin{JID: jid(synGroup), Timestamp: synAt(900), Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}},
		&events.MediaRetry{MessageID: "SYN-UNKNOWN-1", ChatID: jid(synGroup), SenderID: jid(aliceLID), Timestamp: synAt(1000), Error: &events.MediaRetryError{Code: 2}},
	}
}

func recordInto(t *testing.T, dir string, evts []any) {
	t.Helper()
	wrapped := ingest.NewRecorder(dir).Wrap(func(any) {})
	for _, e := range evts {
		wrapped(e)
	}
}

// The committed synthetic scenario is exactly what syntheticEvents records; -update regenerates it.
func TestSyntheticEventsMatchGenerator(t *testing.T) {
	dir := filepath.Join("testdata", "events", "synthetic")
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		recordInto(t, dir, syntheticEvents())
	}
	fresh := t.TempDir()
	recordInto(t, fresh, syntheticEvents())
	names := func(d string) []string {
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
	want, got := names(fresh), names(dir)
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
}

// H14 (e): every recorded scenario replays through the real pipeline into a fresh store to its reviewed golden dump.
func TestReplayMatchesGolden(t *testing.T) {
	scenarios, err := os.ReadDir(filepath.Join("testdata", "events"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no scenarios in testdata/events")
	}
	for _, s := range scenarios {
		if !s.IsDir() {
			continue
		}
		t.Run(s.Name(), func(t *testing.T) {
			evts, err := ingest.Load(filepath.Join("testdata", "events", s.Name()))
			if err != nil {
				t.Fatal(err)
			}
			st := testkit.Store(t)
			if err = ingest.Replay(context.Background(), historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}), evts); err != nil {
				t.Fatal(err)
			}
			got, err := ingest.DumpRaw(st)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", s.Name()+".sql")
			if *update {
				if err = os.MkdirAll(filepath.Dir(golden), 0o755); err == nil {
					err = os.WriteFile(golden, []byte(got), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("replay of %s differs from %s:\n%s", s.Name(), golden, lineDiff(string(want), got))
			}
		})
	}
}

// lineDiff lists the lines only in want (-) and only in got (+).
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for _, l := range w {
		if !slices.Contains(g, l) {
			out = append(out, "- "+l)
		}
	}
	for _, l := range g {
		if !slices.Contains(w, l) {
			out = append(out, "+ "+l)
		}
	}
	return strings.Join(out, "\n")
}
