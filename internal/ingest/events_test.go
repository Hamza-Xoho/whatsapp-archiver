package ingest_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// R11: source_id is the event's own message ID for every event that came from a message, ” for undecryptable.
func TestEventsCarryTheirOwnMessageIDAsSourceID(t *testing.T) {
	key := &waCommon.MessageKey{ID: proto.String("TARGET")}
	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: key, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: text("new")}}
	for name, msg := range map[string]*waE2E.Message{
		"reaction":         {ReactionMessage: &waE2E.ReactionMessage{Key: key, Text: proto.String("👍")}},
		"edit":             edit,
		"revoke":           {ProtocolMessage: &waE2E.ProtocolMessage{Key: key, Type: waE2E.ProtocolMessage_REVOKE.Enum()}},
		"pin":              {PinInChatMessage: &waE2E.PinInChatMessage{Key: key, Type: waE2E.PinInChatMessage_PIN_FOR_ALL.Enum()}},
		"poll_vote":        {PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: key, Vote: &waE2E.PollEncValue{EncPayload: []byte("c"), EncIV: []byte("i")}}},
		"protocol":         {ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_EPHEMERAL_SETTING.Enum()}},
		"secret_encrypted": {SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{TargetMessageKey: key, SecretEncType: waE2E.SecretEncryptedMessage_POLL_EDIT.Enum()}},
		"secret edit":      {SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{TargetMessageKey: key, SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum()}},
	} {
		r := ingest.FromMessage(messageEvent("g@g.us", "OWN-"+name, msg), "live", time.Unix(20, 0))
		if r.Event == nil || r.Event.SourceID != "OWN-"+name {
			t.Errorf("%s: event=%+v", name, r.Event)
		}
	}
}

func TestStubEventCarriesItsOwnIDAsSourceID(t *testing.T) {
	web := webMsg("STUB1", "3@lid", false, nil)
	web.MessageStubType = waWeb.WebMessageInfo_CIPHERTEXT.Enum()
	evt := messageEvent("g@g.us", "STUB1", nil)
	r := ingest.FromStub(evt, web, time.Unix(20, 0))
	if r.Event == nil || r.Event.SourceID != "STUB1" || r.Event.Source != "history" {
		t.Fatalf("event=%+v", r.Event)
	}
}

// R11: the undecryptable event's own ID is its target; its source_id is empty.
func TestUndecryptableHasNoSourceID(t *testing.T) {
	r := ingest.FromUndecryptable(&events.UndecryptableMessage{Info: messageEvent("g@g.us", "U1", nil).Info}, time.Unix(20, 0))
	if r.Event.SourceID != "" || r.Event.TargetID != "U1" {
		t.Fatalf("event=%+v", r.Event)
	}
}

// The cause comes from what whatsmeow tells in the event: the unavailable type of a view-once message, whether the
// sender sent no ciphertext at all (no sender key), or nothing more specific (other).
func TestUndecryptableCause(t *testing.T) {
	for _, c := range []struct {
		name string
		evt  events.UndecryptableMessage
		want string
	}{
		{"view once unavailable", events.UndecryptableMessage{IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce}, "view_once_unavailable"},
		{"view once type alone", events.UndecryptableMessage{UnavailableType: events.UnavailableTypeViewOnce}, "view_once_unavailable"},
		{"no ciphertext, no sender key", events.UndecryptableMessage{IsUnavailable: true}, "no_sender_key"},
		{"decryption failed", events.UndecryptableMessage{}, "other"},
		{"hidden failure", events.UndecryptableMessage{DecryptFailMode: events.DecryptFailHide}, "other"},
	} {
		c.evt.Info = messageEvent("g@g.us", "U2", nil).Info
		r := ingest.FromUndecryptable(&c.evt, time.Unix(20, 0))
		if r.Event.Cause != c.want || !store.ValidEventCause(r.Event.Cause) {
			t.Errorf("%s: cause=%q, want %q", c.name, r.Event.Cause, c.want)
		}
	}
}

func TestUndecryptableCauseIsStored(t *testing.T) {
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	p.Handle(&events.UndecryptableMessage{Info: messageEvent("g@g.us", "U3", nil).Info, IsUnavailable: true, UnavailableType: events.UnavailableTypeViewOnce})
	if got := rows(t, st, `SELECT target_id,source_id,cause FROM message_events`); len(got) != 1 || got[0] != "U3||view_once_unavailable" {
		t.Fatalf("message_events=%v", got)
	}
}

// An edit's text is new_text (the viewer and the search read it there); a vote's options are the value only.
func TestEditTextIsNewTextAndVoteOptionsAreNot(t *testing.T) {
	e := ingest.FromMessage(messageEvent("g@g.us", "ED", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Key: &waCommon.MessageKey{ID: proto.String("T")}, Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), EditedMessage: text("new")}}), "live", time.Unix(20, 0))
	if e.Event.NewText != "new" {
		t.Fatalf("event=%+v", e.Event)
	}
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()).WithVoteDecryptor((&voteFake{hashes: [][]byte{hashOf("Yes")}}).decrypt)
	p.Handle(messageEvent("g@g.us", "POLL", pollMessage("Lunch?", "Yes", "No")))
	p.Handle(voteEvent("PV1", "POLL"))
	if got := rows(t, st, `SELECT new_text,value FROM message_events WHERE kind='poll_vote'`); len(got) != 1 || got[0] != `|{"options":["Yes"]}` {
		t.Fatalf("poll_vote=%v", got)
	}
}

// ---- R14: reactions and poll updates embedded in a history message ----

func hashOf(name string) []byte { h := sha256.Sum256([]byte(name)); return h[:] }

func pollMessage(name string, options ...string) *waE2E.Message {
	poll := &waE2E.PollCreationMessage{Name: proto.String(name)}
	for _, o := range options {
		poll.Options = append(poll.Options, &waE2E.PollCreationMessage_Option{OptionName: proto.String(o)})
	}
	return &waE2E.Message{PollCreationMessage: poll}
}

func TestHistoryReactionsBecomeReactionEvents(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	msg := webMsg("MSG", "3@lid", false, text("liked"))
	msg.Reactions = []*waWeb.Reaction{
		{Key: &waCommon.MessageKey{ID: proto.String("RX1"), Participant: proto.String("4@lid")}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(12_500)},
		{Key: &waCommon.MessageKey{ID: proto.String("RX2"), FromMe: proto.Bool(true)}, Text: proto.String("❤"), SenderTimestampMS: proto.Int64(13_000)},
	}
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", msg)); err != nil {
		t.Fatal(err)
	}
	got := rows(t, st, `SELECT target_id,kind,source_id,source,sender_jid,ts,value FROM message_events ORDER BY source_id`)
	want := []string{"MSG|reaction|RX1|history|4@lid|12|👍", "MSG|reaction|RX2|history|111@s.whatsapp.net|13|❤"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("message_events=%v\nwant %v", got, want)
	}
	if n := rows(t, st, `SELECT id FROM messages`); len(n) != 1 || n[0] != "MSG" {
		t.Fatalf("messages=%v", n)
	}
}

func TestHistoryReactionOnAStubIsStoredToo(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	stub := stubOf("STB", "3@lid", waWeb.WebMessageInfo_CIPHERTEXT)
	stub.Reactions = []*waWeb.Reaction{{Key: &waCommon.MessageKey{ID: proto.String("RX3"), Participant: proto.String("4@lid")}, Text: proto.String("😂")}}
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", stub)); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, st, `SELECT kind,target_id,source_id FROM message_events ORDER BY kind`); len(got) != 2 || got[0] != "reaction|STB|RX3" || got[1] != "stub|STB|STB" {
		t.Fatalf("message_events=%v", got)
	}
}

// A reaction seen live and again in a history chunk is one row: the event ID follows the reaction's own message ID.
func TestLiveThenHistoryReactionIsStoredOnce(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	live := messageEvent("g@g.us", "RX1", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("MSG")}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(12_500)}})
	live.Info.Sender = jid("4@lid")
	p.Handle(live)
	msg := webMsg("MSG", "3@lid", false, text("liked"))
	msg.Reactions = []*waWeb.Reaction{{Key: &waCommon.MessageKey{ID: proto.String("RX1"), Participant: proto.String("4@lid")}, Text: proto.String("👍"), SenderTimestampMS: proto.Int64(12_500)}}
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", msg)); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, st, `SELECT source_id,source FROM message_events`); len(got) != 1 || got[0] != "RX1|live" {
		t.Fatalf("message_events=%v", got)
	}
}

func TestHistoryPollUpdatesBecomePollVoteEvents(t *testing.T) {
	st := testkit.Store(t)
	p := historyPipeline(st, ingest.NewFilter(nil, nil), &queueFake{})
	poll := webMsg("POLL", "3@lid", false, pollMessage("Lunch?", "Yes", "No", "Maybe"))
	poll.PollUpdates = []*waWeb.PollUpdate{
		{PollUpdateMessageKey: &waCommon.MessageKey{ID: proto.String("PV1"), Participant: proto.String("4@lid")}, Vote: &waE2E.PollVoteMessage{SelectedOptions: [][]byte{hashOf("No"), hashOf("Yes")}}, SenderTimestampMS: proto.Int64(15_000)},
		{PollUpdateMessageKey: &waCommon.MessageKey{ID: proto.String("PV2"), Participant: proto.String("5@lid")}, Vote: &waE2E.PollVoteMessage{}, SenderTimestampMS: proto.Int64(16_000)},
	}
	if _, _, err := p.ImportHistory(t.Context(), chunk("g@g.us", poll)); err != nil {
		t.Fatal(err)
	}
	got := rows(t, st, `SELECT target_id,kind,source_id,source,sender_jid,ts,value FROM message_events ORDER BY source_id`)
	want := []string{`POLL|poll_vote|PV1|history|4@lid|15|{"options":["No","Yes"]}`, `POLL|poll_vote|PV2|history|5@lid|16|{"options":[]}`}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("message_events=%v\nwant %v", got, want)
	}
}

// ---- R15: a live poll vote is decrypted in internal/wa and named by the poll's options ----

type voteFake struct {
	hashes [][]byte
	err    error
	seen   []string
}

func (v *voteFake) decrypt(_ context.Context, evt *events.Message) ([][]byte, error) {
	v.seen = append(v.seen, string(evt.Info.ID))
	return v.hashes, v.err
}

func voteEvent(id, poll string) *events.Message {
	e := messageEvent("g@g.us", id, &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{PollCreationMessageKey: &waCommon.MessageKey{ID: proto.String(poll)},
		Vote: &waE2E.PollEncValue{EncPayload: []byte("c"), EncIV: []byte("i")}}})
	e.Info.Sender = types.JID{User: "4", Server: types.HiddenUserServer}
	return e
}

func voteValue(t *testing.T, v *voteFake, withPoll bool) string {
	t.Helper()
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet())
	if v != nil {
		p = p.WithVoteDecryptor(v.decrypt)
	}
	if withPoll {
		p.Handle(messageEvent("g@g.us", "POLL", pollMessage("Lunch?", "Yes", "No", "Maybe")))
	}
	p.Handle(voteEvent("PV1", "POLL"))
	got := rows(t, st, `SELECT value FROM message_events WHERE kind='poll_vote'`)
	if len(got) != 1 {
		t.Fatalf("poll_vote rows=%v", got)
	}
	return got[0]
}

func TestLiveVoteIsNamedByThePollOptions(t *testing.T) {
	v := &voteFake{hashes: [][]byte{hashOf("Maybe"), hashOf("Yes")}}
	if got := voteValue(t, v, true); got != `{"options":["Maybe","Yes"]}` || len(v.seen) != 1 || v.seen[0] != "PV1" {
		t.Fatalf("value=%q seen=%v", got, v.seen)
	}
}

func TestLiveVoteThatClearsAllChoicesHasNoOptions(t *testing.T) {
	if got := voteValue(t, &voteFake{}, true); got != `{"options":[]}` {
		t.Fatalf("value=%q", got)
	}
}

func TestVoteThatCannotBeDecryptedKeepsAnEmptyValue(t *testing.T) {
	if got := voteValue(t, &voteFake{err: errors.New("original message secret not found")}, true); got != "" {
		t.Fatalf("value=%q", got)
	}
}

func TestVoteOnAnUnarchivedPollKeepsAnEmptyValue(t *testing.T) {
	if got := voteValue(t, &voteFake{hashes: [][]byte{hashOf("Yes")}}, false); got != "" {
		t.Fatalf("value=%q", got)
	}
}

func TestVoteForHashesOfNoOptionKeepsAnEmptyValue(t *testing.T) {
	if got := voteValue(t, &voteFake{hashes: [][]byte{hashOf("Not an option")}}, true); got != "" {
		t.Fatalf("value=%q", got)
	}
}

func TestVoteWithoutADecryptorKeepsAnEmptyValue(t *testing.T) {
	if got := voteValue(t, nil, true); got != "" {
		t.Fatalf("value=%q", got)
	}
}

// The decryptor is asked about poll votes only.
func TestVoteDecryptionNeverRunsForOtherMessages(t *testing.T) {
	v := &voteFake{}
	st := testkit.Store(t)
	p := ingest.NewPipeline(st, ingest.NewFilter(nil, nil), &queueFake{}, &healthFake{}, quiet()).WithVoteDecryptor(v.decrypt)
	p.Handle(messageEvent("g@g.us", "T1", text("hello")))
	p.Handle(messageEvent("g@g.us", "R1", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("T1")}, Text: proto.String("👍")}}))
	if len(v.seen) != 0 {
		t.Fatalf("decrypt ran for %v", v.seen)
	}
}
