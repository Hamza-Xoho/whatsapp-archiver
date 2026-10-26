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

// The denylist is pinned: a change to it must change this test too, so it is a deliberate decision. Key names are
// normalised (lower case, no "_" or "-"). A name that contains one of the words is refused, unless it ends in
// "timestamp" (tcTokenTimestamp); the exact names are refused as whole names.
func TestSpoolMetaDenylistIsPinned(t *testing.T) {
	words := []string{"enckey", "mediakey", "password", "privatekey", "secret", "token"}
	names := []string{"enchandle", "fileencsha256", "filesha256", "initialhistbootstrapinlinepayload", "key", "keydata", "passphrase"}
	if got := store.SpoolMetaDenylist(); !slices.Equal(got, words) {
		t.Fatalf("denylist words = %v, want %v", got, words)
	}
	if got := store.SpoolMetaDenyNames(); !slices.Equal(got, names) {
		t.Fatalf("denylist names = %v, want %v", got, names)
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
		"secret as a substring":  `{"messageSecretProof":"x"}`,
		"bot secret":             `{"botMessageSecret":"x"}`,
		"token as a substring":   `{"accessTokens":"x"}`,
		"tc token":               `{"tcToken":"x"}`,
		"token with an expiry":   `{"sessionTokenExpiry":1}`,
		"private key":            `{"myPrivateKeyBytes":"x"}`,
		"enc key":                `{"pollEncKey":"x"}`,
		"inline payload":         `{"initialHistBootstrapInlinePayload":"x"}`,
		"passphrase":             `{"passphrase":"x"}`,
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

// Names that merely contain the letters of a bare name (keyboard, monkey), a token timestamp, directPath and the sizes
// are fine; values are never inspected (a text that says mediaKey is just text).
func TestSpoolReceiveAcceptsKeyFreeMeta(t *testing.T) {
	s := open(t)
	sp := spool("c1")
	sp.Meta = `{"syncType":"FULL","directPath":"/v/t62/abc","fileLength":99,"keyboard":1,"monkey":2,"tcTokenTimestamp":3,"tcTokenSenderTimestamp":4,"note":"mediaKey is just text here","nested":{"chunkOrder":4}}`
	if _, isNew, err := s.SpoolReceive(context.Background(), sp, store.HistoryChunk{ID: "c1", ReceivedAt: 1}); err != nil || !isNew {
		t.Fatalf("SpoolReceive = %v, %v; want a new row", isNew, err)
	}
	empty := spool("c2")
	empty.Meta = ""
	if _, isNew, err := s.SpoolReceive(context.Background(), empty, store.HistoryChunk{ID: "c2", ReceivedAt: 1}); err != nil || !isNew {
		t.Fatalf("empty meta = %v, %v; want it stored as {}", isNew, err)
	}
}
