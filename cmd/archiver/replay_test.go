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
	isolateConfigDir(t)
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

// H14 (e): `replay --dump` prints exactly the reviewed golden dump of the synthetic scenario; without --dump the
// same dump is written to <data>/raw.sql and its path printed.
func TestReplayCommandDumpMatchesGolden(t *testing.T) {
	scenario := filepath.Join("..", "..", "internal", "ingest", "testdata", "events", "synthetic")
	golden, err := os.ReadFile(filepath.Join("..", "..", "internal", "ingest", "testdata", "golden", "synthetic.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"replay", scenario, "--data", t.TempDir(), "--dump"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if stdout.String() != string(golden) {
		t.Fatalf("--dump output differs from the golden dump:\n%s", stdout.String())
	}
	data := t.TempDir()
	stdout.Reset()
	if code := run([]string{"replay", "--data", data, scenario}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	path := filepath.Join(data, "raw.sql")
	written, err := os.ReadFile(path)
	if err != nil || string(written) != string(golden) || !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout=%q, %s matches golden: %v (err %v)", stdout.String(), path, string(written) == string(golden), err)
	}
}

// isolateConfigDir points os.UserConfigDir at a temporary folder, so the default scrub key is never the real one.
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AppData", filepath.Join(home, "appdata"))
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// scrubbedSender scrubs the fixture with the given extra arguments and returns the fake of the live sender.
func scrubbedSender(t *testing.T, in string, extra ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "scrubbed")
	var stdout, stderr bytes.Buffer
	if code := run(append(append([]string{"scrub"}, extra...), in, out), &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	evts, err := ingest.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	return evts[0].(*events.Message).Info.Sender.User
}

// Fix round 1, I3: the fakes are keyed. Without --key-file the key is <config dir>/archiver/scrub.key, created
// once with mode 0600 and reused; --key-file overrides it; another key gives other fakes.
func TestScrubCommandKeyFile(t *testing.T) {
	config := isolateConfigDir(t)
	in := recordFixture(t)
	first := scrubbedSender(t, in)
	defaultKey := filepath.Join(config, "archiver", "scrub.key")
	info, err := os.Stat(defaultKey)
	if err != nil {
		t.Fatalf("default key not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v, want 0600", info.Mode().Perm())
	}
	if again := scrubbedSender(t, in); again != first {
		t.Errorf("second run with the same key: %s, first %s", again, first)
	}
	other := filepath.Join(t.TempDir(), "other.key")
	if fake := scrubbedSender(t, in, "--key-file", other); fake == first {
		t.Errorf("another key gives the same fake %s", fake)
	}
	if again := scrubbedSender(t, in, "--key-file", other); again == first {
		t.Errorf("--key-file not reused")
	}
	if info, err = os.Stat(other); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("--key-file not created with 0600: %v %v", info, err)
	}
}
