package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

func mustJID(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// recordFixture records a live group message and a history chunk with one direct message.
func recordFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rec := ingest.NewRecorder(dir).Wrap(func(any) {})
	rec(&events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: mustJID(t, "10000000100@g.us"), Sender: mustJID(t, "4915123456789:12@s.whatsapp.net"), IsGroup: true},
		ID: "LIVE1", PushName: "Alice", Timestamp: time.Unix(1_700_000_000, 0)}, Message: &waE2E.Message{Conversation: proto.String("hello")}})
	rec(&events.HistorySync{Data: &waHistorySync.HistorySync{Progress: proto.Uint32(100), Conversations: []*waHistorySync.Conversation{{ID: proto.String("20000000003@lid"),
		Messages: []*waHistorySync.HistorySyncMsg{{Message: &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{RemoteJID: proto.String("20000000003@lid"), ID: proto.String("HIST1")},
			MessageTimestamp: proto.Uint64(1_600_000_000), Message: &waE2E.Message{Conversation: proto.String("old")}}}}}}}})
	return dir
}

// H14 (b): `archiver scrub <in> <out>` writes a scrubbed copy that still loads.
func TestScrubCommandWritesScrubbedCopy(t *testing.T) {
	in, out := recordFixture(t), filepath.Join(t.TempDir(), "scrubbed")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scrub", in, out}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 2 {
		t.Fatalf("scrubbed %d files, want 2", len(entries))
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(out, e.Name()))
		if strings.Contains(string(data), "4915123456789") {
			t.Errorf("%s keeps the phone number", e.Name())
		}
	}
	if evts, err := ingest.Load(out); err != nil || len(evts) != 2 {
		t.Fatalf("scrubbed copy loads %d events, err %v", len(evts), err)
	}
	if code := run([]string{"scrub", in}, &stdout, &stderr); code != 2 {
		t.Fatalf("scrub with one argument: code=%d, want 2", code)
	}
}

// H14 (b): `archiver replay <dir> --data <data>` feeds the recorded events through the pipeline into a fresh archive,
// history included, without a WhatsApp session; it refuses an existing archive.
func TestReplayCommandStoresEventsWithoutConnecting(t *testing.T) {
	events, data := recordFixture(t), t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"replay", events, "--data", data}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "replayed 2 events") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	stats, err := st.ChatStats(context.Background())
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, s := range stats {
		got[s.CanonicalJID] = s.MessageCount
	}
	if got["10000000100@g.us"] != 1 || got["20000000003@lid"] != 1 {
		t.Fatalf("messages per chat = %v, want the live and the history message", got)
	}
	if _, err = os.Stat(filepath.Join(data, "session.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("replay touched session.db: %v", err)
	}
	stderr.Reset()
	if code := run([]string{"replay", events, "--data", data}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "fresh") {
		t.Fatalf("replay into an existing archive: code=%d stderr=%q, want a refusal", code, stderr.String())
	}
}
