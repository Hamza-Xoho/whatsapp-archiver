package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"whatsapp-archiver/internal/store"
)

// I3 (Hamza, option b, 26 Oct 2026): the history spool is in the backed-up archive.db, so its Meta JSON must never
// carry key material. SpoolReceive refuses such a Meta; it does not parse the blob.

// The denylist is pinned: a change to it must change this test too, so it is a deliberate decision.
func TestSpoolMetaDenylistIsPinned(t *testing.T) {
	want := []string{
		"botmessagesecret", "enchandle", "enckey", "fileencsha256", "filesha256", "key", "keydata", "mediakey",
		"messagesecret", "passphrase", "password", "privatekey", "secret", "tctoken", "token",
	}
	if got := store.SpoolMetaDenylist(); !slices.Equal(got, want) {
		t.Fatalf("denylist = %v, want %v", got, want)
	}
}

func TestSpoolReceiveRejectsKeyMaterialInMeta(t *testing.T) {
	for name, meta := range map[string]string{
		"media key":              `{"mediaKey":"AAAA"}`,
		"upper case":             `{"MEDIAKEY":"AAAA"}`,
		"snake case":             `{"media_key":"AAAA"}`,
		"kebab case":             `{"file-enc-sha256":"AAAA"}`,
		"file sha":               `{"fileSha256":"AAAA"}`,
		"nested object":          `{"notification":{"inner":{"messageSecret":"AAAA"}}}`,
		"inside an array":        `{"parts":[{"ok":1},{"token":"x"}]}`,
		"array of arrays":        `{"parts":[[{"password":"x"}]]}`,
		"plain key":              `{"key":"x"}`,
		"enc handle":             `{"encHandle":"h"}`,
		"even with a null value": `{"secret":null}`,
		"not json":               `{"size":`,
		"not an object":          `["mediaKey"]`,
	} {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			sp := spool("c1")
			sp.Meta = meta
			_, isNew, err := s.SpoolReceive(context.Background(), sp, store.HistoryChunk{ID: "c1", ReceivedAt: 1})
			if !errors.Is(err, store.ErrConstraint) || isNew {
				t.Fatalf("SpoolReceive = %v, %v; want ErrConstraint and not new", isNew, err)
			}
			if got := text(t, s, "SELECT (SELECT count(*) FROM history_spool)||':'||(SELECT count(*) FROM history_sync_chunks)"); got != "0:0" {
				t.Fatalf("rows after a rejected meta = %q, want none", got)
			}
		})
	}
}

// Names that merely contain a denylisted word are fine, and so are directPath and the sizes: the denylist matches whole
// key names (case-insensitively, ignoring _ and -), not substrings, and never looks at values.
func TestSpoolReceiveAcceptsKeyFreeMeta(t *testing.T) {
	s := open(t)
	sp := spool("c1")
	sp.Meta = `{"syncType":"FULL","directPath":"/v/t62/abc","fileLength":99,"keyboard":1,"monkey":2,"tokens_used":3,"note":"mediaKey is just text here","nested":{"chunkOrder":4}}`
	if _, isNew, err := s.SpoolReceive(context.Background(), sp, store.HistoryChunk{ID: "c1", ReceivedAt: 1}); err != nil || !isNew {
		t.Fatalf("SpoolReceive = %v, %v; want a new row", isNew, err)
	}
	empty := spool("c2")
	empty.Meta = ""
	if _, isNew, err := s.SpoolReceive(context.Background(), empty, store.HistoryChunk{ID: "c2", ReceivedAt: 1}); err != nil || !isNew {
		t.Fatalf("empty meta = %v, %v; want it stored as {}", isNew, err)
	}
}
