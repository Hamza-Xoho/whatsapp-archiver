package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

const (
	familyJID  = "120363111@g.us"
	workJID    = "120363222@g.us"
	laylaPN    = "966550100123@s.whatsapp.net"
	laylaLID   = "204871@lid"
	healthJSON = `{"state":"connected","detail":"","last_event":"2026-10-12T09:15:30Z","phone_last_active":"2026-10-12T06:00:00Z","panics":2,"updated_at":"2026-10-12T09:16:00Z","version":"2.3000.1031","version_reason":"latest"}`
)

func utc(value string) int64 {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t.Unix()
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func msg(chat, id, source, at string) store.Message {
	ts := utc(at)
	return store.Message{ChatJID: chat, ID: id, SenderJID: "s@lid", Source: source, Text: id, Timestamp: ts, ReceivedAt: ts, RawProto: []byte(id)}
}

// seedFolder builds a data folder: three chats (one LID/PN alias pair), history coverage, one open decryption gap,
// media rows in every state, a health.json and a config with a fixed time zone and two excluded chats.
func seedFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	for _, c := range []store.Chat{{JID: familyJID, Name: "Family", Type: "group"}, {JID: laylaPN, Name: "Layla", Type: "user"}, {JID: workJID, Name: "Work", Type: "group"}} {
		mustDo(t, st.UpsertChat(ctx, c))
	}
	for _, m := range []store.Message{
		msg(familyJID, "H1", "history", "2024-03-01T21:30:00Z"), msg(familyJID, "H2", "history", "2024-06-15T10:00:00Z"),
		msg(familyJID, "L1", "live", "2026-10-10T08:00:00Z"), msg(familyJID, "L2", "live", "2026-10-11T20:59:00Z"), msg(familyJID, "L3", "live", "2026-10-12T09:15:30Z"),
		msg(laylaPN, "P1", "history", "2025-01-05T12:00:00Z"), msg(laylaPN, "P2", "live", "2025-01-06T12:00:00Z"),
		msg(laylaLID, "Q1", "live", "2026-10-09T17:00:00Z"), msg(laylaLID, "Q2", "live", "2026-10-12T06:00:00Z"),
		msg(workJID, "W1", "live", "2026-01-01T00:00:00Z"),
	} {
		_, err = st.InsertMessage(ctx, m)
		mustDo(t, err)
	}
	_, err = st.InsertIDMapping(ctx, store.IDMapping{LID: laylaLID, PN: laylaPN, Source: "test", FirstSeen: time.Unix(utc("2026-10-09T17:00:00Z"), 0)})
	mustDo(t, err)
	// one gap stays open (GAP1 never arrived); the one for L2 closed when L2 was stored.
	for _, e := range []store.MessageEvent{{ChatJID: laylaLID, TargetID: "GAP1", EventID: "gap-GAP1"}, {ChatJID: familyJID, TargetID: "L2", EventID: "gap-L2"}} {
		e.Kind, e.Timestamp, e.ReceivedAt = "undecryptable", utc("2026-10-12T05:00:00Z"), utc("2026-10-12T05:00:00Z")
		_, err = st.InsertEvent(ctx, e)
		mustDo(t, err)
	}
	for id, state := range map[string]string{"f1": "failed", "e1": "expired", "r1": "retry_requested", "p1": "pending", "d1": "downloading", "ok": "done"} {
		mustDo(t, st.InsertMedia(ctx, store.Media{ChatJID: familyJID, MessageID: id, Source: "live", State: state}))
	}
	mustDo(t, st.InsertMedia(ctx, store.Media{ChatJID: laylaPN, MessageID: "m1", Source: "history", State: "failed"}))
	for _, h := range []store.HistoryChunk{{ID: "h1", Progress: 20, ReceivedAt: utc("2026-10-10T08:05:00Z")}, {ID: "h2", Progress: 60, ReceivedAt: utc("2026-10-10T08:20:00Z")}, {ID: "h3", Progress: 100, ReceivedAt: utc("2026-10-10T09:00:00Z")}} {
		mustDo(t, st.InsertHistoryChunk(ctx, h))
	}
	mustDo(t, st.Close())
	writeFile(t, filepath.Join(dir, "config.yaml"), "time_zone: Asia/Riyadh\nexclude:\n  - "+laylaPN+"\n  - "+workJID+"\nwa:\n  check_latest_version: false\n")
	writeFile(t, filepath.Join(dir, "health.json"), healthJSON)
	writeFile(t, filepath.Join(dir, "media", "f1.bin"), strings.Repeat("x", 1500))
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mustDo(t, os.MkdirAll(filepath.Dir(path), 0o700))
	mustDo(t, os.WriteFile(path, []byte(content), 0o600))
}

func runCmd(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		mustDo(t, os.MkdirAll("testdata", 0o755))
		mustDo(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing: review the output, then create it once with `go test ./cmd/archiver -update`: %v\n%s", path, err, got)
	}
	if string(want) != got {
		t.Fatalf("output differs from %s\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func TestStatusGolden(t *testing.T) {
	code, out, errOut := runCmd(t, "status", "--data", seedFolder(t))
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	golden(t, "status.golden", out)
}

func TestChatsGolden(t *testing.T) {
	code, out, errOut := runCmd(t, "chats", "--data", seedFolder(t))
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	golden(t, "chats.golden", out)
}

func lines(out string) []string { return strings.Split(strings.TrimRight(out, "\n"), "\n") }

func TestStatusPrintsHealthFieldsInTheConfiguredZone(t *testing.T) {
	code, out, errOut := runCmd(t, "status", "--data", seedFolder(t))
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	got := lines(out)
	for _, want := range []string{
		"state: connected",
		"client version: 2.3000.1031 (latest)",
		"last event: 2026-10-12 12:15 +03",
		"phone last active: 2026-10-12 09:00 +03",
		"history: complete back to 2024-03-02, chunks 3, last chunk 2026-10-10 12:00 +03",
		"recovered panics: 2",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing line %q in\n%s", want, out)
		}
	}
}

func TestStatusChatRowsAndTotals(t *testing.T) {
	_, out, _ := runCmd(t, "status", "--data", seedFolder(t))
	got := lines(out)
	for _, want := range []string{
		"Family\t" + familyJID + "\t5\t2024-03-02 00:30 +03\t2026-10-12 12:15 +03\t1\t2\t2\t0",
		"Layla\t" + laylaLID + "\t4\t2025-01-05 15:00 +03\t2026-10-12 09:00 +03\t1\t0\t0\t1",
		"Work\t" + workJID + "\t1\t2026-01-01 03:00 +03\t2026-01-01 03:00 +03\t0\t0\t0\t0",
		"total\t3 chats\t10\t-\t-\t2\t2\t2\t1",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing line %q in\n%s", want, out)
		}
	}
}

func TestStatusWithoutHealthJSON(t *testing.T) {
	dir := seedFolder(t)
	mustDo(t, os.Remove(filepath.Join(dir, "health.json")))
	code, out, errOut := runCmd(t, "status", "--data", dir)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := lines(out); got[0] != "state: unknown (health.json not found)" {
		t.Fatalf("first line %q", got[0])
	}
}

func TestStatusPrintsDashForUnknownTimesAndVersion(t *testing.T) {
	dir := seedFolder(t)
	mustDo(t, os.Remove(filepath.Join(dir, "health.json")))
	_, out, _ := runCmd(t, "status", "--data", dir)
	got := lines(out)
	for _, want := range []string{"client version: -", "last event: -", "phone last active: -", "recovered panics: 0"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing line %q in\n%s", want, out)
		}
	}
}

func TestStatusEmptyTimeZoneIsUTCAndInvalidIsAnError(t *testing.T) {
	dir := seedFolder(t)
	writeFile(t, filepath.Join(dir, "config.yaml"), "time_zone: \"\"\n")
	code, out, errOut := runCmd(t, "status", "--data", dir)
	if code != 0 || !slices.Contains(lines(out), "last event: 2026-10-12 09:15 UTC") {
		t.Fatalf("code=%d stderr=%q out=\n%s", code, errOut, out)
	}
	writeFile(t, filepath.Join(dir, "config.yaml"), "time_zone: Mars/Olympus\n")
	for _, cmd := range []string{"status", "chats"} {
		code, out, errOut = runCmd(t, cmd, "--data", dir)
		if code == 0 || !strings.Contains(errOut, "Mars/Olympus") {
			t.Fatalf("%s: code=%d stderr=%q out=%q", cmd, code, errOut, out)
		}
	}
}

func TestStatusHistoryLines(t *testing.T) {
	cases := []struct {
		name     string
		progress []int
		want     string
	}{
		{"none", nil, "history: none received yet"},
		{"in progress", []int{30, 55}, "history: in progress (55%), chunks 2, last chunk 2026-10-10 08:10 UTC"},
		{"complete", []int{100, 40}, "history: complete back to 2024-03-01, chunks 2, last chunk 2026-10-10 08:10 UTC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.Open(filepath.Join(dir, "archive.db"))
			mustDo(t, err)
			_, err = st.InsertMessage(context.Background(), msg(familyJID, "H1", "history", "2024-03-01T21:30:00Z"))
			mustDo(t, err)
			for i, p := range c.progress {
				mustDo(t, st.InsertHistoryChunk(context.Background(), store.HistoryChunk{ID: fmt.Sprint("c", i), Progress: p, ReceivedAt: utc("2026-10-10T08:00:00Z") + int64(i)*600}))
			}
			mustDo(t, st.Close())
			writeFile(t, filepath.Join(dir, "config.yaml"), "time_zone: UTC\n")
			code, out, errOut := runCmd(t, "status", "--data", dir)
			if code != 0 || !slices.Contains(lines(out), c.want) {
				t.Fatalf("code=%d stderr=%q want line %q in\n%s", code, errOut, c.want, out)
			}
		})
	}
}

func TestStatusDataFolderSize(t *testing.T) {
	dir := seedFolder(t)
	// a sparse nested file moves the folder into the MB range whatever the size of archive.db is
	pad := filepath.Join(dir, "media", "deep", "pad.bin")
	mustDo(t, os.MkdirAll(filepath.Dir(pad), 0o700))
	f, err := os.Create(pad)
	mustDo(t, err)
	mustDo(t, f.Truncate(3<<20))
	mustDo(t, f.Close())
	var total int64
	mustDo(t, filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		total += info.Size()
		return err
	}))
	_, out, _ := runCmd(t, "status", "--data", dir)
	if want := fmt.Sprintf("data folder: %.1f MB", float64(total)/(1<<20)); !slices.Contains(lines(out), want) {
		t.Fatalf("want line %q in\n%s", want, out)
	}
}

func TestChatsExcludedByAliasOrCanonicalJID(t *testing.T) {
	_, out, _ := runCmd(t, "chats", "--data", seedFolder(t))
	got := lines(out)
	for _, want := range []string{
		familyJID + "\tFamily\t5\texcluded=no",
		workJID + "\tWork\t1\texcluded=yes",
		laylaLID + "\tLayla\t4\texcluded=yes", // only the phone-number alias is in the config
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing line %q in\n%s", want, out)
		}
	}
}

// folderSnapshot records name, size and modification time of everything under dir.
func folderSnapshot(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	mustDo(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %s", path, info.Size(), info.ModTime().UTC().Format(time.RFC3339Nano)))
		return nil
	}))
	return out
}

func TestStatusDoesNotWriteArchive(t *testing.T) {
	for _, mode := range []string{"WAL", "DELETE"} { // DELETE is what a VACUUM INTO snapshot looks like
		t.Run(mode, func(t *testing.T) {
			dir := seedFolder(t)
			path := filepath.Join(dir, "archive.db")
			if mode == "DELETE" {
				db, err := sql.Open("sqlite", "file:"+path)
				mustDo(t, err)
				_, err = db.Exec("PRAGMA journal_mode=DELETE")
				mustDo(t, err)
				mustDo(t, db.Close())
			}
			old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
			mustDo(t, os.Chtimes(path, old, old))
			before := folderSnapshot(t, dir)
			for _, cmd := range []string{"status", "chats"} {
				if code, _, errOut := runCmd(t, cmd, "--data", dir); code != 0 {
					t.Fatalf("%s: code=%d stderr=%q", cmd, code, errOut)
				}
				if after := folderSnapshot(t, dir); !slices.Equal(before, after) {
					t.Fatalf("%s changed the data folder:\nbefore %v\nafter  %v", cmd, before, after)
				}
			}
		})
	}
}

func TestStatusReadsWhileTheServiceHasTheArchiveOpen(t *testing.T) {
	dir := seedFolder(t)
	w, err := store.Open(filepath.Join(dir, "archive.db"))
	mustDo(t, err)
	defer func() { _ = w.Close() }()
	_, err = w.InsertMessage(context.Background(), msg(workJID, "W2", "live", "2026-10-12T10:00:00Z"))
	mustDo(t, err)
	code, out, errOut := runCmd(t, "chats", "--data", dir)
	if code != 0 || !slices.Contains(lines(out), workJID+"\tWork\t2\texcluded=yes") {
		t.Fatalf("code=%d stderr=%q out=\n%s", code, errOut, out)
	}
}

func TestStatusAndChatsOnAMissingArchiveFailAndCreateNothing(t *testing.T) {
	for _, cmd := range []string{"status", "chats"} {
		t.Run(cmd, func(t *testing.T) {
			typo := filepath.Join(t.TempDir(), "typo")
			code, out, errOut := runCmd(t, cmd, "--data", typo)
			if code == 0 || out != "" || !strings.Contains(errOut, "no archive at "+filepath.Join(typo, "archive.db")) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
			}
			if _, err := os.Stat(typo); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s created %s: %v", cmd, typo, err)
			}
		})
	}
}

func TestStatusOutputHasNoTimestampInUnixSeconds(t *testing.T) {
	_, out, _ := runCmd(t, "status", "--data", seedFolder(t))
	if re := regexp.MustCompile(`\b1[0-9]{9}\b`); re.MatchString(out) {
		t.Fatalf("raw unix time in output:\n%s", out)
	}
}
