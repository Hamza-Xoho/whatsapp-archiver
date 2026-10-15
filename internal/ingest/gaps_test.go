package ingest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// H13: gap tests for the planned Task 5 pipeline cases and the Review Focus rows that had none.

// panicWriter wraps the real store and panics on its first `panics` message inserts.
type panicWriter struct {
	*store.Store
	panics int
}

func (w *panicWriter) InsertMessage(ctx context.Context, m store.Message) (bool, error) {
	if w.panics > 0 {
		w.panics--
		panic("boom in writer")
	}
	return w.Store.InsertMessage(ctx, m)
}

// Table 1 / RF10: a panic while handling an event is recovered at the handler boundary, reported once as
// "handler", and the next event is stored.
func TestHandleRecoversPanic(t *testing.T) {
	st := testkit.Store(t)
	h := &healthFake{}
	p := ingest.NewPipeline(&panicWriter{Store: st, panics: 1}, ingest.NewFilter(nil, nil), &queueFake{}, h, quiet())
	p.Handle(messageEvent("g@g.us", "BOOM", text("first")))
	if h.panics != 1 {
		t.Fatalf("panics reported = %d, want 1", h.panics)
	}
	p.Handle(messageEvent("g@g.us", "NEXT", text("second")))
	if got := strings.Join(rows(t, st, "SELECT id,text FROM messages"), ","); got != "NEXT|second" || h.panics != 1 {
		t.Fatalf("messages=%q panics=%d", got, h.panics)
	}
}

// RF8: the LID/phone mapping an event carries is stored before the exclusion filter runs, so an event in the LID
// form of an excluded phone number is recognised and dropped, while the mapping itself is kept.
func TestMappingFromAltJIDStoredBeforeFilter(t *testing.T) {
	st := testkit.Store(t)
	aliases := func(jid string) ([]string, error) { return st.Aliases(context.Background(), jid) }
	p := ingest.NewPipeline(st, ingest.NewFilter([]string{"9665@s.whatsapp.net"}, aliases), &queueFake{}, &healthFake{}, quiet())
	e := messageEvent("1234@lid", "A", text("secret"))
	e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	p.Handle(e)
	if got := strings.Join(rows(t, st, "SELECT lid,pn FROM id_mappings"), ","); got != "1234@lid|9665@s.whatsapp.net" {
		t.Fatalf("mappings=%q", got)
	}
	if got := rows(t, st, "SELECT chat_jid,id FROM messages"); len(got) != 0 {
		t.Fatalf("excluded chat stored: %v", got)
	}
	if got := rows(t, st, "SELECT jid FROM chats"); len(got) != 0 {
		t.Fatalf("excluded chat recorded: %v", got)
	}
}

// RF8, the other direction: an excluded LID also excludes its phone-number form, once a mapping is known. The
// mapping arrives with an event of the excluded chat itself.
func TestExcludeLIDMatchesPhoneForm(t *testing.T) {
	f := ingest.NewFilter([]string{"1@lid"}, func(j string) ([]string, error) {
		if j == "1@s.whatsapp.net" {
			return []string{"1@lid"}, nil
		}
		return nil, nil
	})
	if keep(t, f, "1@s.whatsapp.net", ingest.KindMessage) || keep(t, f, "1@lid", ingest.KindMessage) {
		t.Fatal("excluded chat kept")
	}

	st := testkit.Store(t)
	aliases := func(jid string) ([]string, error) { return st.Aliases(context.Background(), jid) }
	p := ingest.NewPipeline(st, ingest.NewFilter([]string{"1234@lid"}, aliases), &queueFake{}, &healthFake{}, quiet())
	learn := messageEvent("1234@lid", "LEARN", text("x"))
	learn.Info.RecipientAlt = jid("9665@s.whatsapp.net")
	p.Handle(learn)
	p.Handle(messageEvent("9665@s.whatsapp.net", "PHONE", text("secret")))
	if got := rows(t, st, "SELECT chat_jid,id FROM messages"); len(got) != 0 {
		t.Fatalf("phone form of an excluded LID stored: %v", got)
	}
}

// Table 1: the same DM seen under its LID and its phone-number form is stored once, whichever arrives first.
func TestSameMessageUnderLIDAndPhoneStoredOnce(t *testing.T) {
	lidWithAlt := func() *events.Message {
		e := messageEvent("1234@lid", "SAME", text("hello"))
		e.Info.RecipientAlt = jid("9665@s.whatsapp.net")
		return e
	}
	cases := []struct {
		name  string
		order []*events.Message
		want  string
	}{
		{"LID first", []*events.Message{lidWithAlt(), messageEvent("9665@s.whatsapp.net", "SAME", text("hello"))}, "1234@lid|SAME"},
		{"phone first", []*events.Message{messageEvent("9665@s.whatsapp.net", "SAME", text("hello")), lidWithAlt()}, "9665@s.whatsapp.net|SAME"},
	}
	for _, c := range cases {
		st := testkit.Store(t)
		p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
		for _, e := range c.order {
			p.Handle(e)
		}
		if got := strings.Join(rows(t, st, "SELECT chat_jid,id FROM messages"), ","); got != c.want {
			t.Fatalf("%s: messages=%q, want %q", c.name, got, c.want)
		}
		if stats, err := st.ChatStats(t.Context()); err != nil || len(stats) != 1 || stats[0].MessageCount != 1 {
			t.Fatalf("%s: resolved view stats=%+v err=%v", c.name, stats, err)
		}
	}
}

// activityHealth records the fromMe flag of every Seen call.
type activityHealth struct {
	healthFake
	fromMe []bool
}

func (h *activityHealth) Seen(_ time.Time, fromMe bool) { h.fromMe = append(h.fromMe, fromMe) }

// RF: a message sent from the user's own phone counts as phone activity (fromMe true); one from a contact does not.
func TestFromMeUpdatesPhoneActivity(t *testing.T) {
	h := &activityHealth{}
	p := ingest.NewPipeline(testkit.Store(t), ingest.NewFilter(nil, nil), &queueFake{}, h, quiet())
	own := messageEvent("g@g.us", "OWN", text("mine"))
	own.Info.IsFromMe = true
	p.Handle(own)
	p.Handle(messageEvent("g@g.us", "THEIRS", text("theirs")))
	if len(h.fromMe) != 2 || !h.fromMe[0] || h.fromMe[1] {
		t.Fatalf("Seen fromMe flags = %v, want [true false]", h.fromMe)
	}
}

// RF: an event the archiver does not handle (noise, or a type it has never heard of) changes nothing.
func TestUnknownEventIsIgnored(t *testing.T) {
	st := testkit.Store(t)
	h := &healthFake{}
	q := &queueFake{}
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), q, h, quiet())
	for _, evt := range []any{&events.Receipt{}, &events.Presence{}, &events.ChatPresence{}, &events.CallOffer{}, struct{ Surprise int }{1}, nil} {
		p.Handle(evt)
	}
	if h.state != "" || h.detail != "" || h.seen != 0 || h.failed != 0 || h.panics != 0 || len(q.items) != 0 {
		t.Fatalf("health=%+v queued=%v", h, q.items)
	}
	for _, table := range []string{"chats", "participants", "id_mappings", "messages", "message_events", "chat_events", "history_sync_chunks", "media"} {
		if got := rows(t, st, "SELECT COUNT(*) FROM "+table); got[0] != "0" {
			t.Fatalf("%s has %s rows after unknown events", table, got[0])
		}
	}
}

// RF: edits, revokes and reactions whose target was never stored are kept as events; no message row is invented.
func TestEventsForUnknownTargetAreStored(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	edit := protocolMsg(waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), "NEVER")
	edit.EditedMessage = text("fixed")
	p.Handle(messageEvent("g@g.us", "E1", &waE2E.Message{ProtocolMessage: edit}))
	p.Handle(messageEvent("g@g.us", "R1", &waE2E.Message{ProtocolMessage: protocolMsg(waE2E.ProtocolMessage_REVOKE.Enum(), "NEVER")}))
	p.Handle(messageEvent("g@g.us", "X1", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("NEVER")}, Text: proto.String("+")}}))
	got := strings.Join(rows(t, st, "SELECT target_id,kind,new_text FROM message_events ORDER BY kind"), ",")
	if got != "NEVER|edit|fixed,NEVER|reaction|+,NEVER|revoke|" {
		t.Fatalf("events=%q", got)
	}
	if n := rows(t, st, "SELECT id FROM messages"); len(n) != 0 {
		t.Fatalf("messages=%v", n)
	}
}

// RF: the exclusion applies to every kind of event, not just messages; the noise kinds are dropped everywhere.
func TestScopeDropsExcludedChatForEveryKind(t *testing.T) {
	kinds := []ingest.Kind{
		ingest.KindMessage, ingest.KindEdit, ingest.KindRevoke, ingest.KindProtocol, ingest.KindReaction, ingest.KindPin,
		ingest.KindPollVote, ingest.KindUndecryptable, ingest.KindSecretEncrypted, ingest.KindGroupInfo, ingest.KindParticipants,
		ingest.KindAppState, ingest.KindPresence, ingest.KindTyping, ingest.KindReceipt, ingest.KindCall,
	}
	noise := map[ingest.Kind]bool{ingest.KindPresence: true, ingest.KindTyping: true, ingest.KindReceipt: true, ingest.KindCall: true}
	f := ingest.NewFilter([]string{"secret@g.us"}, nil)
	for _, k := range kinds {
		if keep(t, f, "secret@g.us", k) {
			t.Errorf("kind %s of an excluded chat was kept", k)
		}
		if got := keep(t, f, "open@g.us", k); got == noise[k] {
			t.Errorf("kind %s of a normal chat: kept=%v", k, got)
		}
	}
}

// Task 6: one chunk of two conversations, three messages in the kept one and two in the excluded one: three are
// stored, two skipped, the chunk is one ledger row, and nothing of the excluded chat is left behind.
func TestHistoryChunkSplitsKeptAndExcludedConversations(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter([]string{"secret@g.us"}, nil), &queueFake{})
	hs := chunk("kept@g.us", webMsg("K1", "3@lid", false, text("a")), webMsg("K2", "3@lid", false, text("b")), webMsg("K3", "3@lid", false, text("c")))
	hs.Data.Conversations = append(hs.Data.Conversations, chunk("secret@g.us", webMsg("S1", "4@lid", false, text("x")), webMsg("S2", "4@lid", false, text("y"))).Data.Conversations...)
	stored, skipped, err := p.ImportHistory(t.Context(), hs)
	if err != nil || stored != 3 || skipped != 2 {
		t.Fatalf("stored=%d skipped=%d err=%v", stored, skipped, err)
	}
	if got := strings.Join(rows(t, st, "SELECT chat_jid,id FROM messages ORDER BY id"), ","); got != "kept@g.us|K1,kept@g.us|K2,kept@g.us|K3" {
		t.Fatalf("messages=%s", got)
	}
	coverage, err := st.HistoryCoverage(t.Context())
	if err != nil || coverage.Chunks != 1 {
		t.Fatalf("coverage=%+v err=%v", coverage, err)
	}
	if got := rows(t, st, "SELECT message_count,conversation_count FROM history_sync_chunks"); strings.Join(got, ",") != "5|2" {
		t.Fatalf("ledger row = %v", got)
	}
}

// Table 1 / RF9: messages of a chunk that arrives after the history reached 100% are stored with their ids and text.
func TestHistoryMessagesOfLateChunkAreStored(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	if _, _, err := p.ImportHistory(t.Context(), history("g@g.us", "EARLY", 100)); err != nil {
		t.Fatal(err)
	}
	if cov, err := st.HistoryCoverage(t.Context()); err != nil || !cov.Complete {
		t.Fatalf("coverage after the first chunk = %+v err=%v", cov, err)
	}
	late := history("g@g.us", "LATE", 100)
	late.Data.Conversations[0].Messages[0].Message.MessageTimestamp = proto.Uint64(5)
	if stored, _, err := p.ImportHistory(t.Context(), late); err != nil || stored != 1 {
		t.Fatalf("late chunk stored=%d err=%v", stored, err)
	}
	if got := strings.Join(rows(t, st, "SELECT id,text,ts,source FROM messages ORDER BY id"), ","); got != "EARLY|EARLY|10|history,LATE|LATE|5|history" {
		t.Fatalf("messages=%s", got)
	}
	if cov, err := st.HistoryCoverage(t.Context()); err != nil || cov.Chunks != 2 {
		t.Fatalf("coverage=%+v err=%v", cov, err)
	}
}

// Task 3: document, audio (voice note and plain), video and gif each yield a media row of their kind.
func TestDocumentAudioAndVideoMedia(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		kind string
		mime string
		text string
	}{
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), Caption: proto.String("notes")}}, "document", "application/pdf", "notes"},
		{"voice note", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true)}}, "audio", "audio/ogg; codecs=opus", ""},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/mpeg")}}, "audio", "audio/mpeg", ""},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), Caption: proto.String("clip")}}, "video", "video/mp4", "clip"},
		{"gif", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true)}}, "gif", "video/mp4", ""},
	}
	for _, c := range cases {
		r := ingest.FromMessage(messageEvent("1@lid", "M", c.msg), "live", time.Unix(20, 0))
		if r.Media == nil || r.Media.Type != c.kind || r.Media.MIME != c.mime || r.Message == nil || r.Message.MediaType != c.kind || r.Message.Text != c.text {
			t.Fatalf("%s: media=%+v message=%+v", c.name, r.Media, r.Message)
		}
	}
}

// Task 3: a view-once image in history is unwrapped like a live one: its media is queued.
func TestHistoryViewOnceIsUnwrapped(t *testing.T) {
	st := testkit.Store(t)
	q := &queueFake{}
	p := historyPipeline(st, ingest.NewFilter(nil, nil), q)
	once := &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("look")}}}}
	if stored, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", webMsg("ONCE", "3@lid", false, once))); err != nil || stored != 1 {
		t.Fatalf("stored=%d err=%v", stored, err)
	}
	if got := strings.Join(rows(t, st, "SELECT id,text,media_type FROM messages"), ","); got != "ONCE|look|image" || len(q.items) != 1 || q.items[0].Type != "image" {
		t.Fatalf("messages=%s media=%+v", got, q.items)
	}
}

// retryQueue records the media-retry events routed to the queue.
type retryQueue struct {
	queueFake
	retries []*events.MediaRetry
}

func (q *retryQueue) OnRetry(_ context.Context, e *events.MediaRetry) {
	q.retries = append(q.retries, e)
}

// RF: a media-retry notification reaches the media queue through Handle.
func TestHandleRoutesMediaRetryToTheQueue(t *testing.T) {
	q := &retryQueue{}
	p := ingest.NewPipeline(testkit.Store(t), ingest.NewFilter(nil, nil), q, &healthFake{}, quiet())
	e := &events.MediaRetry{MessageID: "IMG"}
	p.Handle(e)
	if len(q.retries) != 1 || q.retries[0] != e {
		t.Fatalf("retries routed = %v", q.retries)
	}
}
