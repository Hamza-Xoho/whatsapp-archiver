package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"

	"whatsapp-archiver/internal/store"
)

func TestInsertMessageIsIdempotent(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	m := message("120363@g.us", "ABC", time.Unix(10, 0))
	ok1, err := s.InsertMessage(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	ok2, err := s.InsertMessage(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if !ok1 || ok2 {
		t.Fatalf("inserted=%v,%v", ok1, ok2)
	}
	stats, _ := s.ChatStats(ctx)
	if len(stats) != 1 || stats[0].MessageCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestInsertMessageUnderKnownAliasIsDuplicate(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, _ = s.InsertMessage(ctx, message("9665@s.whatsapp.net", "X", time.Unix(1, 0)))
	_, _ = s.InsertIDMapping(ctx, store.IDMapping{LID: "1234@lid", PN: "9665@s.whatsapp.net", Source: "test", FirstSeen: time.Unix(2, 0)})
	ok, err := s.InsertMessage(ctx, message("1234@lid", "X", time.Unix(3, 0)))
	if err != nil || ok {
		t.Fatalf("inserted=%v err=%v", ok, err)
	}
}

func TestAliasLearnedLaterCollapsesInView(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, _ = s.InsertMessage(ctx, message("9665@s.whatsapp.net", "X", time.Unix(1, 0)))
	_, _ = s.InsertMessage(ctx, message("1234@lid", "X", time.Unix(2, 0)))
	_, _ = s.InsertIDMapping(ctx, store.IDMapping{LID: "1234@lid", PN: "9665@s.whatsapp.net", Source: "test"})
	stats, err := s.ChatStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].MessageCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestEventForUnknownMessageIsStored(t *testing.T) {
	s := open(t)
	ok, err := s.InsertEvent(context.Background(), store.MessageEvent{ChatJID: "120363@g.us", TargetID: "NOPE", Kind: "edit", EventID: "E1", NewText: "x"})
	if err != nil || !ok {
		t.Fatalf("inserted=%v err=%v", ok, err)
	}
}

func TestIDMappingIsIdempotent(t *testing.T) {
	s := open(t)
	m := store.IDMapping{LID: "1@lid", PN: "1@s.whatsapp.net"}
	a, _ := s.InsertIDMapping(context.Background(), m)
	b, _ := s.InsertIDMapping(context.Background(), m)
	if !a || b {
		t.Fatalf("inserted=%v,%v", a, b)
	}
}

func TestOpenGapClosesWhenMessageArrives(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, _ = s.InsertEvent(ctx, store.MessageEvent{ChatJID: "1@lid", TargetID: "X", Kind: "undecryptable", EventID: "gap-X"})
	if n, _ := s.OpenGapCount(ctx); n != 1 {
		t.Fatalf("gaps=%d", n)
	}
	_, _ = s.InsertMessage(ctx, message("1@lid", "X", time.Unix(1, 0)))
	if n, _ := s.OpenGapCount(ctx); n != 0 {
		t.Fatalf("gaps=%d", n)
	}
}

func TestWriteErrorIsReturned(t *testing.T) {
	s := open(t)
	_ = s.Close()
	if _, err := s.InsertMessage(context.Background(), message("1@lid", "X", time.Time{})); err == nil {
		t.Fatal("expected error")
	}
}

func TestChatStatsOldestNewest(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i, sec := range []int64{2, 1, 3} {
		_, _ = s.InsertMessage(ctx, message("g@g.us", string(rune('A'+i)), time.Unix(sec, 0)))
	}
	stats, _ := s.ChatStats(ctx)
	if len(stats) != 1 || stats[0].Oldest != 1 || stats[0].Newest != 3 || stats[0].MessageCount != 3 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestTimestampStoredAsGiven(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, _ = s.InsertMessage(ctx, message("g@g.us", "future", time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)))
	_, _ = s.InsertMessage(ctx, message("g@g.us", "zero", time.Unix(0, 0)))
	stats, _ := s.ChatStats(ctx)
	if stats[0].Oldest != 0 || stats[0].Newest != 4070908800 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestReopenRunsMigrationsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.SchemaVersionCount(context.Background()); err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
}

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func message(chat, id string, ts time.Time) store.Message {
	return store.Message{ChatJID: chat, ID: id, SenderJID: "sender@lid", Timestamp: ts.Unix(), ReceivedAt: ts.Unix(), Source: "live", Text: id, RawProto: []byte(id)}
}

func TestInsertEventTwiceIsNotAnError(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	e := store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1", Timestamp: 1, ReceivedAt: 1}
	first, err := s.InsertEvent(ctx, e)
	if err != nil || !first {
		t.Fatalf("first inserted=%v err=%v", first, err)
	}
	second, err := s.InsertEvent(ctx, e)
	if err != nil || second {
		t.Fatalf("second inserted=%v err=%v", second, err)
	}
}

func TestInsertIDMappingTwiceIsNotAnError(t *testing.T) {
	s := open(t)
	m := store.IDMapping{LID: "1@lid", PN: "1@s.whatsapp.net"}
	if ok, err := s.InsertIDMapping(context.Background(), m); err != nil || !ok {
		t.Fatalf("first inserted=%v err=%v", ok, err)
	}
	if ok, err := s.InsertIDMapping(context.Background(), m); err != nil || ok {
		t.Fatalf("second inserted=%v err=%v", ok, err)
	}
}

func TestConstraintViolationIsClassified(t *testing.T) {
	s := open(t)
	m := message("g@g.us", "A", time.Unix(1, 0))
	m.Source = "not-a-source" // violates the CHECK on messages.source
	_, err := s.InsertMessage(context.Background(), m)
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err=%v", err)
	}
}

func TestOtherErrorsAreNotConstraintErrors(t *testing.T) {
	s := open(t)
	_ = s.Close()
	_, err := s.InsertEvent(context.Background(), store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1"})
	if err == nil || errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err=%v", err)
	}
}

// failInserts makes every insert into table run body through a trigger installed from a second connection.
func failInserts(t *testing.T, s *store.Store, table, body string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec("CREATE TRIGGER fail_" + table + " BEFORE INSERT ON " + table + " BEGIN " + body + "; END"); err != nil {
		t.Fatal(err)
	}
}

// B1/H2 carry-over: every write classifies constraint errors, not only insert() and the message insert.
func TestUpsertAndMediaConstraintErrorsAreClassified(t *testing.T) {
	writes := map[string]func(*store.Store) error{
		"chats": func(s *store.Store) error { return s.UpsertChat(context.Background(), store.Chat{JID: "g@g.us"}) },
		"participants": func(s *store.Store) error {
			return s.UpsertParticipant(context.Background(), store.Participant{JID: "1@lid"})
		},
		"media": func(s *store.Store) error {
			return s.InsertMedia(context.Background(), store.Media{ChatJID: "g@g.us", MessageID: "A", State: "pending"})
		},
	}
	for table, write := range writes {
		t.Run(table, func(t *testing.T) {
			s := open(t)
			failInserts(t, s, table, "SELECT RAISE(ABORT, 'rejected')")
			if err := write(s); !errors.Is(err, store.ErrConstraint) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestConstraintThroughInsertIsClassified(t *testing.T) {
	s := open(t)
	failInserts(t, s, "message_events", "SELECT RAISE(ABORT, 'rejected')")
	_, err := s.InsertEvent(context.Background(), store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1"})
	if !errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err=%v", err)
	}
}

func TestOtherSQLiteErrorIsNotAConstraintError(t *testing.T) {
	s := open(t)
	failInserts(t, s, "message_events", "SELECT abs(-9223372036854775807 - 1)")
	_, err := s.InsertEvent(context.Background(), store.MessageEvent{ChatJID: "g@g.us", TargetID: "A", Kind: "reaction", EventID: "E1"})
	var se *sqlite.Error
	if !errors.As(err, &se) || errors.Is(err, store.ErrConstraint) {
		t.Fatalf("err=%v", err)
	}
}

func TestCoverageReportsOldestPerChat(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, m := range []store.Message{message("g@g.us", "A", time.Unix(5, 0)), message("g@g.us", "B", time.Unix(3, 0)), message("9665@s.whatsapp.net", "C", time.Unix(7, 0)), message("1234@lid", "D", time.Unix(4, 0)), message("g@g.us", "LIVE", time.Unix(1, 0))} {
		if m.ID != "LIVE" {
			m.Source = "history"
		}
		if _, err := s.InsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.InsertIDMapping(ctx, store.IDMapping{LID: "1234@lid", PN: "9665@s.whatsapp.net"})
	for i, progress := range []int{40, 90} {
		if err := s.InsertHistoryChunk(ctx, store.HistoryChunk{ID: string(rune('a' + i)), Progress: progress, ReceivedAt: int64(100 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.HistoryCoverage(ctx)
	if err != nil || c.Chunks != 2 || c.LastProgress != 90 || c.LastChunkTime != 101 || len(c.OldestByChat) != 2 || c.OldestByChat["g@g.us"] != 3 || c.OldestByChat["1234@lid"] != 4 {
		t.Fatalf("coverage=%+v err=%v", c, err)
	}
}
