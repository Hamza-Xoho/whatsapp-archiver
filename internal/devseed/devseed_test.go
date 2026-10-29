package devseed_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	"whatsapp-archiver/internal/devseed"
	"whatsapp-archiver/internal/store"
)

func TestSmallSeedHasExpectedKindsAndCounts(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	report, err := devseed.Seed(context.Background(), devseed.Options{
		DataDir:  data,
		Chats:    5,
		Messages: 2000,
		Seed:     42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Chats != 5 || report.Messages != 2000 {
		t.Fatalf("report = %+v, want 5 chats and 2000 messages", report)
	}
	if len(report.KindCounts) != len(store.MessageKinds) {
		t.Fatalf("kind counts = %v, want every kind", report.KindCounts)
	}
	for _, kind := range store.MessageKinds {
		if got := report.KindCounts[kind]; got != 125 {
			t.Errorf("%s count = %d, want 125", kind, got)
		}
	}

	db := openDB(t, filepath.Join(data, "archive.db"))
	defer db.Close()
	assertCount(t, db, "messages", 2000)
	assertCount(t, db, "chat_stats", 5)
	assertAtLeast(t, db, "id_mappings", 2)
	assertAtLeast(t, db, "open_gaps", 1)
	assertAtLeast(t, db, "connection_periods", 1)
	assertTextExists(t, db, `text GLOB '*[ًٌٍَُِّْ]*'`, "Arabic diacritics")
	assertTextExists(t, db, `text LIKE '%ـ%'`, "Arabic tatweel")
	assertTextExists(t, db, `text GLOB '*[A-Za-z]*'`, "English text")

	files, err := filepath.Glob(filepath.Join(data, "media", "devseed", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 7 {
		t.Fatalf("media files = %d, want at least one for each media kind", len(files))
	}
	for _, name := range files {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 || info.Size() > 4096 {
			t.Errorf("media file %s has size %d, want 1..4096", name, info.Size())
		}
	}

	before := derivedState(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(data, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RebuildDerived(context.Background()); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	afterDB := openDB(t, filepath.Join(data, "archive.db"))
	defer afterDB.Close()
	if after := derivedState(t, afterDB); after != before {
		t.Fatalf("RebuildDerived differs from incremental state\n--- incremental\n%s\n--- rebuilt\n%s", before, after)
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	var dumps []string
	for i := 0; i < 2; i++ {
		data := filepath.Join(t.TempDir(), fmt.Sprintf("data-%d", i))
		if _, err := devseed.Seed(context.Background(), devseed.Options{DataDir: data, Chats: 3, Messages: 64, Seed: 9}); err != nil {
			t.Fatal(err)
		}
		st, err := store.OpenReadOnly(filepath.Join(data, "archive.db"))
		if err != nil {
			t.Fatal(err)
		}
		dump, err := st.DumpRaw(context.Background())
		_ = st.Close()
		if err != nil {
			t.Fatal(err)
		}
		dumps = append(dumps, dump)
	}
	if dumps[0] != dumps[1] {
		t.Fatal("the same seed produced different raw archives")
	}
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func assertAtLeast(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got < want {
		t.Fatalf("%s count = %d, want at least %d", table, got, want)
	}
}

func assertTextExists(t *testing.T, db *sql.DB, where, label string) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE " + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Errorf("seed has no %s", label)
	}
}

func derivedState(t *testing.T, db *sql.DB) string {
	t.Helper()
	queries := []string{
		`SELECT alias,canonical FROM chat_canonical ORDER BY alias`,
		`SELECT canonical,name,type,messages,last_ts,oldest_ts,COALESCE(oldest_history_ts,-1),media_done,media_failed,media_expired,media_pending,media_bytes,gaps_open FROM chat_stats ORDER BY canonical`,
	}
	var out strings.Builder
	for _, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&out, values)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"search_fts", "search_tri"} {
		vocab := "seed_vocab_" + table
		if _, err := db.Exec(`CREATE VIRTUAL TABLE temp.` + vocab + ` USING fts5vocab(main, '` + table + `', 'row')`); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`SELECT term, doc, cnt FROM temp.` + vocab + ` ORDER BY term`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var term string
			var doc, count int
			if err = rows.Scan(&term, &doc, &count); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&out, "%s|%s|%d|%d\n", table, term, doc, count)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}
