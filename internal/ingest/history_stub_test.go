package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// rejectingWriter wraps the real store and rejects one message ID with a constraint error, as SQLite would. A
// rejection retried past a cap panics, so a pipeline that retries it forever ends the test at once.
type rejectingWriter struct {
	*store.Store
	id    string
	tries int
}

func (w *rejectingWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if m.ID == w.id {
		if w.tries++; w.tries > 100 {
			panic("a rejected write was retried")
		}
		return false, fmt.Errorf("insert message %s: %w", m.ID, store.ErrConstraint)
	}
	return w.Store.InsertMessage(ctx, m)
}

// stubChunk is one DM conversation [A text, STUB, C text] followed by a second conversation with message D.
func stubChunk() *events.HistorySync {
	stub := webMsg("STUB", "", false, nil)
	stub.MessageStubType = waWeb.WebMessageInfo_CIPHERTEXT.Enum()
	hs := chunk("4915111@s.whatsapp.net", webMsg("A", "", false, text("a")), stub, webMsg("C", "", false, text("c")))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("120363@g.us", webMsg("D", "3@lid", false, text("d"))).Data.Conversations...)
	return hs
}

// C1: a bodyless history stub (CIPHERTEXT, REVOKE, GROUP_PARTICIPANT_ADD …) is archived as a stub event, logged at Info
// with its stub type; no message row is made for it. The rest of its conversation and the later conversations of the
// chunk are stored.
func TestHistoryStubIsSkippedAndTheChunkGoesOn(t *testing.T) {
	st := testkit.Store(t)
	var buf bytes.Buffer
	h := &healthFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(&buf, nil))).WithHistoryParser(historyParser())
	stored, skipped, err := p.ImportHistory(t.Context(), stubChunk())
	if err != nil || stored != 4 || skipped != 0 || h.failed != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v writeFailed=%d, want 4, 0, nil, 0", stored, skipped, err, h.failed)
	}
	if got := strings.Join(rows(t, st, "SELECT id FROM messages ORDER BY id"), ","); got != "A,C,D" {
		t.Fatalf("messages=%s", got)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,target_id,kind,value FROM message_events"), ","); got != "4915111@s.whatsapp.net|STUB|stub|CIPHERTEXT" {
		t.Fatalf("message_events=%s", got)
	}
	var stubLine string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "STUB") {
			stubLine = line
		}
	}
	if !strings.Contains(stubLine, "level=INFO") || !strings.Contains(stubLine, "CIPHERTEXT") {
		t.Fatalf("stub log line=%q, want an INFO line naming the stub type", stubLine)
	}
}

// stubOf is a bodyless history message of one stub type, as WhatsApp sends revokes, group changes and ciphertext markers.
func stubOf(id, participant string, stub waWeb.WebMessageInfo_StubType) *waWeb.WebMessageInfo {
	m := webMsg(id, participant, false, nil)
	m.MessageStubType = stub.Enum()
	return m
}

// A history revoke stub is one stub event under its key ID, with the stub type as its value, and no message row.
func TestHistoryRevokeStubIsArchivedAsStubEvent(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	stored, skipped, err := p.ImportHistory(t.Context(), chunk("g@g.us", stubOf("REV", "3@lid", waWeb.WebMessageInfo_REVOKE)))
	if err != nil || stored != 1 || skipped != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v, want 1, 0, nil", stored, skipped, err)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,target_id,kind,event_id,sender_jid,value FROM message_events"), ","); got != "g@g.us|REV|stub|REV|3@lid|REVOKE" {
		t.Fatalf("message_events=%s", got)
	}
	if got := rows(t, st, "SELECT id FROM messages"); len(got) != 0 {
		t.Fatalf("messages=%v, want none for a stub", got)
	}
}

// A history group-change stub is one stub event whose value names the group change type.
func TestHistoryGroupChangeStubIsArchivedAsStubEvent(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	stored, skipped, err := p.ImportHistory(t.Context(), chunk("g@g.us", stubOf("ADD", "3@lid", waWeb.WebMessageInfo_GROUP_PARTICIPANT_ADD)))
	if err != nil || stored != 1 || skipped != 0 {
		t.Fatalf("stored=%d skipped=%d err=%v, want 1, 0, nil", stored, skipped, err)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,target_id,kind,value FROM message_events"), ","); got != "g@g.us|ADD|stub|GROUP_PARTICIPANT_ADD" {
		t.Fatalf("message_events=%s", got)
	}
	if got := rows(t, st, "SELECT id FROM messages"); len(got) != 0 {
		t.Fatalf("messages=%v, want none for a stub", got)
	}
}

// A history ciphertext stub is a stub event with value CIPHERTEXT. It is not a gap: open_gaps is kind undecryptable only.
func TestHistoryCiphertextStubIsStubEventAndNotAnOpenGap(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	if _, _, err := p.ImportHistory(t.Context(), chunk("4915111@s.whatsapp.net", stubOf("CT", "", waWeb.WebMessageInfo_CIPHERTEXT))); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,target_id,kind,value FROM message_events"), ","); got != "4915111@s.whatsapp.net|CT|stub|CIPHERTEXT" {
		t.Fatalf("message_events=%s", got)
	}
	if gaps := rows(t, st, "SELECT target_id FROM open_gaps"); len(gaps) != 0 {
		t.Fatalf("open_gaps=%v, want none for a history ciphertext stub", gaps)
	}
}

// The scope filter applies to stubs: a stub of an excluded chat leaves no row of any kind, while a stub of a kept chat
// in the same chunk is archived.
func TestHistoryStubInExcludedChatIsNotStored(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{"secret@g.us"})), nil), &queueFake{})
	hs := chunk("g@g.us", stubOf("KEPT", "3@lid", waWeb.WebMessageInfo_REVOKE))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("secret@g.us", stubOf("SECRET", "4@lid", waWeb.WebMessageInfo_REVOKE)).Data.Conversations...)
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 1 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v, want 1, 1, nil", stored, skipped, err)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,target_id FROM message_events"), ","); got != "g@g.us|KEPT" {
		t.Fatalf("message_events=%s", got)
	}
	if chats, participants := rows(t, st, "SELECT jid FROM chats WHERE jid='secret@g.us'"), rows(t, st, "SELECT jid FROM participants WHERE jid='4@lid'"); len(chats)+len(participants) != 0 {
		t.Fatalf("excluded chat left chats=%v participants=%v", chats, participants)
	}
}

// Re-importing a chunk that holds a stub does not store the stub a second time.
func TestHistoryStubReimportedIsStoredOnce(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	for i := 0; i < 2; i++ {
		if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", stubOf("REV", "3@lid", waWeb.WebMessageInfo_REVOKE))); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(rows(t, st, "SELECT target_id,kind FROM message_events"), ","); got != "REV|stub" {
		t.Fatalf("message_events=%s, want one stub row", got)
	}
}

// The stub's raw proto is the WebMessageInfo, but never its message secret: that is key material, and archive.db is backed up.
func TestHistoryStubRawProtoHoldsNoMessageSecret(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	stub := stubOf("REV", "3@lid", waWeb.WebMessageInfo_REVOKE)
	stub.MessageSecret = []byte("message-secret-0123456789abcdef")
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", stub)); err != nil {
		t.Fatal(err)
	}
	raw := rows(t, st, "SELECT raw_proto FROM message_events")
	if len(raw) != 1 {
		t.Fatalf("message_events raw_proto rows=%d, want 1", len(raw))
	}
	var got waWeb.WebMessageInfo
	if err := proto.Unmarshal([]byte(raw[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.GetKey().GetID() != "REV" || got.GetMessageStubType() != waWeb.WebMessageInfo_REVOKE || len(got.GetMessageSecret()) != 0 {
		t.Fatalf("raw proto id=%q stub=%v secret=%d bytes, want REV, REVOKE, none", got.GetKey().GetID(), got.GetMessageStubType(), len(got.GetMessageSecret()))
	}
}

// C1: a message the store rejects permanently is logged at Error and skipped; the chunk is not aborted.
func TestHistoryRejectedMessageDoesNotAbortTheChunk(t *testing.T) {
	st := testkit.Store(t)
	var buf bytes.Buffer
	h := &healthFake{}
	w := &rejectingWriter{Store: st, id: "C"}
	p := ingest.NewPipeline(w, ingest.NewFilter(nil, nil), &queueFake{}, h, slog.New(slog.NewTextHandler(&buf, nil))).WithHistoryParser(historyParser())
	hs := chunk("4915111@s.whatsapp.net", webMsg("A", "", false, text("a")), webMsg("C", "", false, text("c")), webMsg("E", "", false, text("e")))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("120363@g.us", webMsg("D", "3@lid", false, text("d"))).Data.Conversations...)
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 3 || skipped != 1 {
		t.Fatalf("stored=%d skipped=%d err=%v, want 3, 1, nil", stored, skipped, err)
	}
	if got := strings.Join(rows(t, st, "SELECT id FROM messages ORDER BY id"), ","); got != "A,D,E" {
		t.Fatalf("messages=%s", got)
	}
	if out := buf.String(); !strings.Contains(out, "level=ERROR") || h.failed != 1 {
		t.Fatalf("writeFailed=%d log=%q, want one alert and an ERROR line", h.failed, out)
	}
}

// C1: a message event with no body is stored with an empty raw_proto, never NULL, which the store rejects.
func TestBodylessMessageIsStoredWithEmptyRawProto(t *testing.T) {
	st := testkit.Store(t)
	h := &healthFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, h, quiet())
	p.Handle(messageEvent("g@g.us", "BODYLESS", nil))
	if got := strings.Join(rows(t, st, "SELECT id, length(raw_proto) FROM messages"), ","); got != "BODYLESS|0" || h.failed != 0 {
		t.Fatalf("messages=%q writeFailed=%d, want BODYLESS|0 and no failure", got, h.failed)
	}
}

// stuckWriter wraps the real store and fails every write of one message ID with a transient error. Past a cap it
// panics, so a retry loop that ignores shutdown ends the test at once instead of spinning on the fake clock.
type stuckWriter struct {
	*store.Store
	id    string
	tries int
}

func (w *stuckWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if m.ID == w.id {
		if w.tries++; w.tries > 1000 {
			panic("write retried after the serve context ended")
		}
		return false, errors.New("database is locked")
	}
	return w.Store.InsertMessage(ctx, m)
}

// C1: a stopped serve context still ends the chunk: once a failing write is abandoned at shutdown, the later
// messages and conversations are not written.
func TestHistoryChunkEndsWhenTheServeContextStops(t *testing.T) {
	st := testkit.Store(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ft := newFakeTime()
	sleeps := 0
	ft.onTick = func() {
		if sleeps++; sleeps == 8 {
			cancel()
		}
	}
	p := newPipeline(&stuckWriter{Store: st, id: "A"}, &queueFake{}, &healthFake{}, ft).WithContext(ctx)
	hs := chunk("4915111@s.whatsapp.net", webMsg("A", "", false, text("a")), webMsg("C", "", false, text("c")))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("120363@g.us", webMsg("D", "3@lid", false, text("d"))).Data.Conversations...)
	stored, _, err := p.ImportHistory(t.Context(), hs)
	if err == nil || stored != 0 {
		t.Fatalf("stored=%d err=%v, want 0 and the abandoned write's error", stored, err)
	}
	if got := rows(t, st, "SELECT id FROM messages"); len(got) != 0 {
		t.Fatalf("messages=%v written after shutdown", got)
	}
}
