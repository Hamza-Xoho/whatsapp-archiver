package ingest_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
	"whatsapp-archiver/internal/wa"
)

// historyParser is the real whatsmeow-backed parser for an account with these own IDs.
func historyParser() ingest.HistoryParser {
	return wa.NewHistoryParser(jid("111:5@s.whatsapp.net"), jid("222:5@lid"))
}
func historyPipeline(st ingest.Writer, f *ingest.Filter, q ingest.MediaQueue) *ingest.Pipeline {
	return ingest.NewPipeline(st, f, q, &healthFake{}, quiet()).WithHistoryParser(historyParser())
}

// rows returns the query result, one "a|b|c" string per row.
func rows(t *testing.T, st *store.Store, query string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	cols, _ := r.Columns()
	var out []string
	for r.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = r.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func chunk(chat string, msgs ...*waWeb.WebMessageInfo) *events.HistorySync {
	items := make([]*waHistorySync.HistorySyncMsg, len(msgs))
	for i, m := range msgs {
		items[i] = &waHistorySync.HistorySyncMsg{Message: m}
	}
	return &events.HistorySync{Data: &waHistorySync.HistorySync{Progress: proto.Uint32(1), Conversations: []*waHistorySync.Conversation{{ID: proto.String(chat), Messages: items}}}}
}
func webMsg(id, participant string, fromMe bool, msg *waE2E.Message) *waWeb.WebMessageInfo {
	key := &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(fromMe)}
	if participant != "" {
		key.Participant = proto.String(participant)
	}
	return &waWeb.WebMessageInfo{Key: key, MessageTimestamp: proto.Uint64(10), Message: msg}
}

// H5a: wrapped history messages are unwrapped like live ones: text is kept and media is queued.
func TestHistoryEphemeralAndDocumentCaptionAreUnwrapped(t *testing.T) {
	st := testkit.Store(t)
	q := &queueFake{}
	p := historyPipeline(st, ingest.NewFilter(nil, nil), q)
	eph := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: text("disappearing hello")}}
	doc := &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), Caption: proto.String("syllabus")}}}}
	stored, skipped, err := p.ImportHistory(t.Context(), chunk("g@g.us", webMsg("EPH", "3@lid", false, eph), webMsg("DOC", "3@lid", false, doc)))
	if err != nil || stored != 2 || skipped != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	got := strings.Join(rows(t, st, "SELECT id,text,media_type FROM messages ORDER BY id"), ",")
	if got != "DOC|syllabus|document,EPH|disappearing hello|" || len(q.items) != 1 || q.items[0].MessageID != "DOC" {
		t.Fatalf("messages=%s media=%+v", got, q.items)
	}
}

// H5b: our own history messages are sent by our own JID, never by the contact or the group.
func TestHistoryFromMeMessageHasOwnJIDAsSender(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	for _, hs := range []*events.HistorySync{chunk("4915111@s.whatsapp.net", webMsg("DM", "", true, text("hi"))), chunk("120363@g.us", webMsg("GRP", "", true, text("hi")))} {
		if stored, _, err := p.ImportHistory(t.Context(), hs); err != nil || stored != 1 {
			t.Fatalf("stored=%d err=%v", stored, err)
		}
	}
	got := strings.Join(rows(t, st, "SELECT id,sender_jid FROM messages ORDER BY id"), ",")
	participants := strings.Join(rows(t, st, "SELECT jid FROM participants ORDER BY jid"), ",")
	if got != "DM|111@s.whatsapp.net,GRP|111@s.whatsapp.net" || participants != "111@s.whatsapp.net" {
		t.Fatalf("senders=%s participants=%s", got, participants)
	}
}

// H5c: the push name of a history message is stored for its sender.
func TestHistoryPushNameIsStored(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	m := webMsg("A", "3@lid", false, text("hi"))
	m.PushName = proto.String("Alice")
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", m)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(t, st, "SELECT jid,display_name FROM participants"), ","); got != "3@lid|Alice" {
		t.Fatalf("participants=%s", got)
	}
}

// H5: an edit in history stays an edit event under its target; it is not rewritten into the original message.
func TestHistoryEditIsStillAnEvent(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	edit := &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: &waCommon.MessageKey{ID: proto.String("ORIG")}, EditedMessage: text("fixed"),
	}}}}
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", webMsg("EDIT1", "3@lid", false, edit))); err != nil {
		t.Fatal(err)
	}
	evts := strings.Join(rows(t, st, "SELECT target_id,kind,new_text FROM message_events"), ",")
	messages := rows(t, st, "SELECT id FROM messages")
	if evts != "ORIG|edit|fixed" || len(messages) != 0 {
		t.Fatalf("events=%s messages=%v", evts, messages)
	}
}

// A message the parser rejects (a group message with no sender) is skipped and logged; the chunk goes on.
func TestHistoryUnparseableMessageIsSkippedAndLogged(t *testing.T) {
	st := testkit.Store(t)
	var buf bytes.Buffer
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, slog.New(slog.NewTextHandler(&buf, nil))).WithHistoryParser(historyParser())
	stored, skipped, err := p.ImportHistory(t.Context(), chunk("g@g.us", webMsg("NOSENDER", "", false, text("x")), webMsg("OK", "3@lid", false, text("y"))))
	if err != nil || stored != 1 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "g@g.us") || !strings.Contains(out, "NOSENDER") {
		t.Fatalf("log=%q", out)
	}
}

func TestHistoryThenLiveNoDuplicate(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	hs := history("g@g.us", "ABC", 1)
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 1 || skipped != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	p.Handle(messageEvent("g@g.us", "ABC", text("live")))
	stats, _ := st.ChatStats(t.Context())
	if stats[0].MessageCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}
func TestLateChunkAfterCompleteIsStored(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	a := history("g@g.us", "A", 100)
	b := history("g@g.us", "B", 50)
	_, _, _ = p.ImportHistory(t.Context(), a)
	_, _, _ = p.ImportHistory(t.Context(), b)
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 2 || messageCount(t, st) != 2 {
		t.Fatalf("coverage=%+v err=%v messages=%d", coverage, err, messageCount(t, st))
	}
}
func TestEmptyOrPartialChunkDoesNotPanic(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	stored, skipped, err := p.ImportHistory(t.Context(), &events.HistorySync{Data: &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{nil, {Messages: []*waHistorySync.HistorySyncMsg{{}}}}}})
	if err != nil || stored != 0 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
}
func history(chat, id string, progress uint32) *events.HistorySync {
	return &events.HistorySync{Data: &waHistorySync.HistorySync{
		Progress: proto.Uint32(progress),
		Conversations: []*waHistorySync.Conversation{{
			ID: proto.String(chat),
			Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{
				Key:              &waCommon.MessageKey{RemoteJID: proto.String(chat), ID: proto.String(id), Participant: proto.String("2@lid")},
				MessageTimestamp: proto.Uint64(10), Message: text(id),
			}}},
		}},
	}}
}

// H6a: a chat name learned from history survives the next live message, which carries no name.
func TestLiveMessageKeepsKnownChatName(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	hs := history("g@g.us", "H1", 100)
	hs.Data.Conversations[0].Name = proto.String("Study Group")
	if _, _, err := p.ImportHistory(t.Context(), hs); err != nil {
		t.Fatal(err)
	}
	p.Handle(messageEvent("g@g.us", "L1", text("live")))
	if got := strings.Join(rows(t, st, "SELECT jid,name FROM chats"), ","); got != "g@g.us|Study Group" {
		t.Fatalf("chats=%s", got)
	}
}

// H6b: nothing of an excluded chat is stored from history, not even its JID or name.
func TestExcludedChatNameNotStoredFromHistory(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter([]string{"secret@g.us"}, nil), &queueFake{})
	hs := history("secret@g.us", "S1", 100)
	hs.Data.Conversations[0].Name = proto.String("Private Therapy Group")
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 0 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	if chats, messages := rows(t, st, "SELECT jid,name FROM chats"), rows(t, st, "SELECT id FROM messages"); len(chats)+len(messages) != 0 {
		t.Fatalf("chats=%v messages=%v", chats, messages)
	}
}

// H9: two different chunks with the same type, order, progress and size, received at the same instant, are both
// in the ledger.
func TestTwoDistinctChunksWithSameProgressAreBothLedgered(t *testing.T) {
	st := testkit.Store(t)
	p := newPipeline(st, &queueFake{}, &healthFake{}, newFakeTime())
	for _, hs := range []*events.HistorySync{history("g@g.us", "A", 100), history("g@g.us", "B", 100)} {
		if _, _, err := p.ImportHistory(t.Context(), hs); err != nil {
			t.Fatal(err)
		}
	}
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 2 || messageCount(t, st) != 2 {
		t.Fatalf("coverage=%+v err=%v messages=%d", coverage, err, messageCount(t, st))
	}
}

// A DM held under both its LID and phone forms in one chunk is one conversation once the chunk's mappings are stored.
func TestMappingsInChunkApplyToItsMessages(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	hs := chunk("1234@lid", webMsg("A", "", false, text("a")))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("9665@s.whatsapp.net", webMsg("B", "", false, text("b"))).Data.Conversations...)
	hs.Data.PhoneNumberToLidMappings = []*waHistorySync.PhoneNumberToLIDMapping{{LidJID: proto.String("1234@lid"), PnJID: proto.String("9665@s.whatsapp.net")}}
	if stored, _, err := p.ImportHistory(t.Context(), hs); err != nil || stored != 2 {
		t.Fatalf("stored=%d err=%v", stored, err)
	}
	stats, err := st.ChatStats(t.Context())
	if err != nil || len(stats) != 1 || stats[0].MessageCount != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

// Far-future and zero history timestamps are stored exactly as given, and the import carries on past them.
func TestNonsensicalHistoryTimestampStoredAsGiven(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	future, zero, normal := webMsg("FUTURE", "3@lid", false, text("f")), webMsg("ZERO", "3@lid", false, text("z")), webMsg("NORMAL", "3@lid", false, text("n"))
	future.MessageTimestamp = proto.Uint64(4070908800)
	zero.MessageTimestamp = proto.Uint64(0)
	if stored, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", future, zero, normal)); err != nil || stored != 3 {
		t.Fatalf("stored=%d err=%v", stored, err)
	}
	if got := strings.Join(rows(t, st, "SELECT id,ts FROM messages ORDER BY id"), ","); got != "FUTURE|4070908800,NORMAL|10,ZERO|0" {
		t.Fatalf("messages=%s", got)
	}
}

// H6b: a push name learned from history survives the next message from that sender, which carries none.
func TestEmptyPushNameKeepsKnownDisplayName(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	m := webMsg("H1", "2@lid", false, text("hi"))
	m.PushName = proto.String("Alice")
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", m)); err != nil {
		t.Fatal(err)
	}
	p.Handle(messageEvent("g@g.us", "L1", text("live")))
	if got := strings.Join(rows(t, st, "SELECT jid,display_name FROM participants"), ","); got != "2@lid|Alice" {
		t.Fatalf("participants=%s", got)
	}
}

// H9: the same chunk delivered twice, at different times, is one ledger row and its messages are stored once.
func TestIdenticalChunkTwiceIsOneLedgerRow(t *testing.T) {
	st := testkit.Store(t)
	ft := newFakeTime()
	p := newPipeline(st, &queueFake{}, &healthFake{}, ft)
	for i := 0; i < 2; i++ {
		if _, _, err := p.ImportHistory(t.Context(), history("g@g.us", "A", 100)); err != nil {
			t.Fatal(err)
		}
		ft.clock.Advance(time.Hour)
	}
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 1 || messageCount(t, st) != 1 {
		t.Fatalf("coverage=%+v err=%v messages=%d", coverage, err, messageCount(t, st))
	}
}

// H6b: an excluded chat's history leaves no sender in participants and no media row.
func TestExcludedHistoryChatLeavesNoParticipantsOrMedia(t *testing.T) {
	st := testkit.Store(t)
	q := &queueFake{}
	p := historyPipeline(st, ingest.NewFilter([]string{"secret@g.us"}, nil), q)
	img := webMsg("IMG", "3@lid", false, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}})
	img.PushName = proto.String("Therapist")
	if _, skipped, err := p.ImportHistory(t.Context(), chunk("secret@g.us", img, webMsg("TXT", "4@lid", false, text("x")))); err != nil || skipped != 2 {
		t.Fatalf("skipped=%d err=%v", skipped, err)
	}
	participants, media := rows(t, st, "SELECT jid FROM participants"), rows(t, st, "SELECT message_id FROM media")
	if len(participants)+len(media)+len(q.items) != 0 {
		t.Fatalf("participants=%v media=%v queued=%v", participants, media, q.items)
	}
}

// H5: our own message in a LID-addressed DM is sent by our own LID.
func TestHistoryFromMeInLIDChatHasOwnLIDAsSender(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	if stored, _, err := p.ImportHistory(t.Context(), chunk("4444@lid", webMsg("DM", "", true, text("hi")))); err != nil || stored != 1 {
		t.Fatalf("stored=%d err=%v", stored, err)
	}
	if got := strings.Join(rows(t, st, "SELECT id,sender_jid FROM messages"), ","); got != "DM|222@lid" {
		t.Fatalf("messages=%s", got)
	}
}
