package store_test

import (
	"testing"

	"whatsapp-archiver/internal/store"
)

// searchArchive holds one message per case of the spike 8 table; seq is the order below (1 to 10), the edit is event 1.
func searchArchive(t *testing.T) *store.Store {
	t.Helper()
	s := open(t)
	c := "s@g.us"
	apply(t, s, mb(c, "M1", "text", "قرأت كِتَاب جميل", 10))                                                                               // 1
	apply(t, s, mb(c, "M2", "text", "المدرسة الجديدة", 11))                                                                                // 2
	apply(t, s, mb(c, "M3", "text", "في البيت", 12))                                                                                       // 3
	apply(t, s, mb(c, "M4", "text", "OK then, Football?", 13))                                                                             // 4
	apply(t, s, mb(c, "M5", "poll", "Lunch?", 14, func(m *store.Message) { m.Details = `{"options":["Pizza","شاورما"]}` }))                // 5
	apply(t, s, mb(c, "M6", "location", "", 15, func(m *store.Message) { m.Details = `{"name":"Café Riyadh","address":"King Fahd Rd"}` })) // 6
	apply(t, s, mb(c, "M7", "contacts", "Bob", 16, func(m *store.Message) {
		m.Details = `{"display_name":"Bob","contacts":[{"display_name":"Bob Smith"},{"display_name":"Zainab"}]}`
	})) // 7
	apply(t, s, withMedia(mb(c, "M8", "document", "", 17, func(m *store.Message) { m.Caption = "see attached" }), "pending", 9, "Quarterly Report.pdf")) // 8
	apply(t, s, mb(c, "M9", "unknown", "", 18, func(m *store.Message) { m.Details = `{"fields":["x"]}` }))                                               // 9: no body
	apply(t, s, mb(c, "M10", "contact", "", 19, func(m *store.Message) { m.Details = `{"display_name":"Yusuf"}` }))                                      // 10
	apply(t, s, ev(c, "M2", "edit", "E1", 20, func(e *store.MessageEvent) { e.NewText = "المدرسة القديمة" }))                                            // -1
	apply(t, s, ev(c, "M1", "reaction", "E2", 21, func(e *store.MessageEvent) { e.NewText = "thumbsup" }))                                               // not indexed
	apply(t, s, ev(c, "M1", "pin", "E3", 22, func(e *store.MessageEvent) { e.NewText = "pinnedforever" }))                                               // not indexed
	apply(t, s, ev(c, "M1", "revoke", "E4", 23))                                                                                                         // M1 stays
	return s
}

// Spike 8: the query is folded like the text; word search in search_fts, substrings and attached prefixes in search_tri.
func TestSearchFollowsTheSpike8Table(t *testing.T) {
	s := searchArchive(t)
	cases := []struct {
		name, table, query, want string
	}{
		{"diacritics-free query finds diacritized text (word)", "search_fts", "كتاب", "1"},
		{"diacritics-free query finds diacritized text (trigram)", "search_tri", "كتاب", "1"},
		{"the diacritized query finds it too", "search_fts", "كِتَاب", "1"},
		{"ta marbuta folded", "search_fts", "المدرسه", "-1,2"},
		{"attached prefix: word index misses it", "search_fts", "مدرسه", ""},
		{"attached prefix: trigram finds it, edit included", "search_tri", "مدرسه", "-1,2"},
		{"substring in the middle of a word", "search_tri", "درس", "-1,2"},
		{"case folded", "search_fts", "FOOTBALL", "4"},
		{"two characters in the word index", "search_fts", "في", "3"},
		{"two characters are too short for trigram", "search_tri", "في", ""},
		{"ok in the word index", "search_fts", "ok", "4"},
		{"english substring in trigram", "search_tri", "ootba", "4"},
		{"poll option", "search_fts", "pizza", "5"},
		{"arabic poll option", "search_fts", "شاورما", "5"},
		{"location name, folded case", "search_fts", "CAFÉ", "6"},
		{"location address", "search_tri", "fahd", "6"},
		{"contact name from the details", "search_fts", "smith", "7"},
		{"second contact", "search_fts", "zainab", "7"},
		{"single contact name from the details", "search_fts", "yusuf", "10"},
		{"document filename from the media row", "search_fts", "report", "8"},
		{"document caption", "search_fts", "attached", "8"},
		{"the edit text is indexed under the negative seq", "search_fts", "القديمه", "-1"},
		{"the replaced text is still found under the message", "search_fts", "الجديده", "2"},
		{"a reaction's new_text is not indexed", "search_fts", "thumbsup", ""},
		{"a pin's new_text is not indexed", "search_tri", "pinnedforever", ""},
		{"a revoked message stays searchable", "search_fts", "كتاب", "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(t, s, c.table, c.query); got != c.want {
				t.Errorf("%s MATCH %q = %q, want %q", c.table, c.query, got, c.want)
			}
		})
	}
	db := rawDB(t, s)
	for _, fts := range []string{"search_fts", "search_tri"} {
		// Indexed: seq 1 to 8, 10 and the edit; not the bodyless unknown message (9), nor the reaction, pin or revoke.
		if got := docs(t, db, fts); got != "-1,1,2,3,4,5,6,7,8,10" {
			t.Errorf("%s holds docs %q, want -1,1,2,3,4,5,6,7,8,10", fts, got)
		}
	}
}
