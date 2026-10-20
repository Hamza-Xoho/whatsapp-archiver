package ingest_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/ingest"
)

const (
	excludedPN    = "10000000001@s.whatsapp.net" // an excluded chat, by phone number
	excludedLID   = "20000000001@lid"            // the LID form of excludedPN
	excludedGroup = "10000000100@g.us"
	keptPN        = "10000000002@s.whatsapp.net"
	keptGroup     = "10000000101@g.us"
)

// knownAliases is the store's alias lookup: it knows the LID form of excludedPN and nothing else.
func knownAliases(j string) ([]string, error) {
	if j == excludedLID {
		return []string{excludedPN}, nil
	}
	return nil, nil
}

// recordedFiles lists the files in dir; a folder that was never created holds none.
func recordedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Events of an excluded chat are handed on to the handler but never written, whatever their kind.
func TestRecorderSkipsEventsOfExcludedChat(t *testing.T) {
	dir := t.TempDir()
	ts := time.Unix(1_700_000_500, 0).UTC()
	rec := ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN, excludedGroup})), nil))
	var passed int
	wrapped := rec.Wrap(func(any) bool { passed++; return true })
	excluded := []any{
		messageEvent(excludedPN, "A", text("in a phone-form chat")),
		&events.UndecryptableMessage{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: jid(excludedGroup)}}},
		&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: jid(excludedGroup)}},
		&events.GroupInfo{JID: jid(excludedGroup), Timestamp: ts},
		&events.Archive{JID: jid(excludedGroup), Timestamp: ts},
		&events.Mute{JID: jid(excludedGroup), Timestamp: ts},
		&events.Pin{JID: jid(excludedGroup), Timestamp: ts},
		&events.MediaRetry{ChatID: jid(excludedGroup), MessageID: "M1", Timestamp: ts},
	}
	for _, e := range excluded {
		wrapped(e)
	}
	if passed != len(excluded) {
		t.Fatalf("handler got %d events, want %d", passed, len(excluded))
	}
	if files := recordedFiles(t, dir); len(files) != 0 {
		t.Fatalf("events of an excluded chat were written: %v", files)
	}
}

// Events of a chat that is not excluded are written as before.
func TestRecorderWritesEventsOfKeptChat(t *testing.T) {
	dir := t.TempDir()
	wrapped := ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), knownAliases)).Wrap(func(any) bool { return true })
	wrapped(messageEvent(keptPN, "K", text("kept")))
	wrapped(&events.GroupInfo{JID: jid(keptGroup), Timestamp: time.Unix(1_700_000_500, 0).UTC()})
	if files := recordedFiles(t, dir); len(files) != 2 {
		t.Fatalf("recorded %d files %v, want 2", len(files), files)
	}
}

// The LID form of an excluded phone number is excluded too: the store's alias lookup names the pair.
func TestRecorderSkipsLIDFormOfExcludedPhone(t *testing.T) {
	dir := t.TempDir()
	wrapped := ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), knownAliases)).Wrap(func(any) bool { return true })
	wrapped(messageEvent(excludedLID, "L", text("same person, LID chat")))
	if files := recordedFiles(t, dir); len(files) != 0 {
		t.Fatalf("the LID form of an excluded chat was written: %v", files)
	}
}

// A message can name the pair itself (RecipientAlt), before the store has learned it from this very event.
func TestRecorderSkipsChatWhoseAliasTheEventNames(t *testing.T) {
	dir := t.TempDir()
	evt := messageEvent(excludedLID, "R", text("alias named in the event"))
	evt.Info.RecipientAlt = jid(excludedPN)
	ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), nil)).Wrap(func(any) bool { return true })(evt)
	if files := recordedFiles(t, dir); len(files) != 0 {
		t.Fatalf("an event naming an excluded alias was written: %v", files)
	}
}

// An event whose chat cannot be determined is written as before: no chat, nothing to exclude.
func TestRecorderWritesEventWithoutChat(t *testing.T) {
	dir := t.TempDir()
	wrapped := ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), knownAliases)).Wrap(func(any) bool { return true })
	wrapped(&events.MediaRetry{MessageID: "M8", Timestamp: time.Unix(1_700_000_500, 0).UTC(), Error: &events.MediaRetryError{Code: 2}})
	wrapped(&events.HistorySync{Data: &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum()}})
	if files := recordedFiles(t, dir); len(files) != 2 {
		t.Fatalf("recorded %d files %v, want the 2 events without a chat", len(files), files)
	}
}

// A history chunk carries many chats: the conversations of excluded chats are left out of the recording, and the
// handler still gets the chunk unchanged. An LID conversation is excluded through the chunk's own phone mapping.
func TestRecorderDropsConversationsOfExcludedChatsFromHistory(t *testing.T) {
	dir := t.TempDir()
	hs := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{
			{PnJID: proto.String(excludedPN), LidJID: proto.String(excludedLID)},
		},
		Conversations: []*waHistorySync.Conversation{
			{ID: proto.String(excludedPN), Name: proto.String("excluded by phone")},
			{ID: proto.String(excludedLID), Name: proto.String("excluded by its LID")},
			{ID: proto.String(keptPN), Name: proto.String("kept")},
		},
	}
	evt := &events.HistorySync{Data: hs}
	var got any
	ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), nil)).Wrap(func(v any) bool { got = v; return true })(evt)
	if got != evt {
		t.Fatal("history chunk not passed on unchanged")
	}
	if len(hs.GetConversations()) != 3 {
		t.Fatalf("the handler's chunk lost conversations: %d left", len(hs.GetConversations()))
	}
	loaded, err := ingest.Load(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("loaded %d events, err %v; want the one chunk", len(loaded), err)
	}
	recorded := loaded[0].(*events.HistorySync).Data.GetConversations()
	if len(recorded) != 1 || recorded[0].GetID() != keptPN {
		t.Fatalf("recorded conversations %v, want only %s", recorded, keptPN)
	}
}

// When the alias lookup fails the chat's exclusion is unknown, so the event is not written; it is logged.
func TestRecorderSkipsEventWhenAliasLookupFails(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	failing := func(string) ([]string, error) { return nil, errors.New("store busy") }
	evt := messageEvent(keptPN, "F", text("exclusion unknown"))
	var got any
	rec := ingest.NewRecorder(dir).WithLogger(slog.New(slog.NewTextHandler(&logs, nil))).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), failing))
	rec.Wrap(func(v any) bool { got = v; return true })(evt)
	if got != evt {
		t.Fatal("event not passed on")
	}
	if files := recordedFiles(t, dir); len(files) != 0 {
		t.Fatalf("an event with unknown exclusion was written: %v", files)
	}
	if !strings.Contains(logs.String(), "not recorded") {
		t.Fatalf("logs = %q, want the skipped recording logged", logs.String())
	}
}

// The phone-number mappings of a chunk name excluded contacts too; a recorded chunk must not carry them.
func TestRecorderDropsMappingsOfExcludedChatsFromHistory(t *testing.T) {
	dir := t.TempDir()
	hs := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{
			{PnJID: proto.String(excludedPN), LidJID: proto.String(excludedLID)},
			{PnJID: proto.String(keptPN), LidJID: proto.String("30000000002@lid")},
		},
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(keptPN)}},
	}
	ingest.NewRecorder(dir).WithFilter(ingest.NewFilter(ingest.NewRuleSet(ingest.ExclusionRules([]string{excludedPN})), nil)).Wrap(func(any) bool { return true })(&events.HistorySync{Data: hs})
	loaded, err := ingest.Load(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("loaded %d events, err %v; want the one chunk", len(loaded), err)
	}
	mappings := loaded[0].(*events.HistorySync).Data.GetPhoneNumberToLidMappings()
	if len(mappings) != 1 || mappings[0].GetPnJID() != keptPN {
		t.Fatalf("recorded mappings %v, want only the kept contact %s", mappings, keptPN)
	}
}
