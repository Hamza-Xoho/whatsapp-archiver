package store_test

import (
	"fmt"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func searchMessage(chat, id, sender, kind, text string, ts int64) store.Message {
	return store.Message{ChatJID: chat, ID: id, SenderJID: sender, Kind: kind, Text: text,
		Source: "live", Timestamp: ts, ReceivedAt: ts, RawProto: []byte(id)}
}

func addSearchMessage(t *testing.T, s *store.Store, m store.Message) {
	t.Helper()
	if _, err := s.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestSearchRoutesFoldedTermsAndEscapesQuotes(t *testing.T) {
	s := open(t)
	addSearchMessage(t, s, searchMessage("family@g.us", "arabic", "1@lid", "text", "المَدْرَسَة الجديدة", 40))
	addSearchMessage(t, s, searchMessage("family@g.us", "short", "1@lid", "text", "أنا في البيت", 30))
	addSearchMessage(t, s, searchMessage("family@g.us", "quoted", "1@lid", "text", `She said "hello" today`, 20))

	cases := []struct {
		query, want string
	}{
		{"مدرسه", "arabic"}, // three or more runes: trigram substring, folded Arabic
		{"في", "short"},     // one/two runes: unicode word index
		{`said "hello"`, "quoted"},
	}
	for _, tc := range cases {
		hits, err := s.Search(t.Context(), store.SearchParams{Query: tc.query})
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.query, err)
		}
		if len(hits) != 1 || hits[0].ID != tc.want {
			t.Errorf("Search(%q) = %+v, want %s", tc.query, hits, tc.want)
		}
	}
	if hits, err := s.Search(t.Context(), store.SearchParams{Query: " \t\n "}); err != nil || len(hits) != 0 {
		t.Fatalf("blank Search = %+v, %v; want no FTS query and no hits", hits, err)
	}
}

func TestSearchCanonicalResolutionFiltersBadgesAndCursor(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "7@lid", PN: "700@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertIDMapping(ctx, store.IDMapping{LID: "2@lid", PN: "200@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(ctx, store.Chat{JID: "7@lid", Name: "Family", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertParticipant(ctx, store.Participant{JID: "2@lid", PushName: "Layla"}); err != nil {
		t.Fatal(err)
	}
	first := searchMessage("700@s.whatsapp.net", "first", "200@s.whatsapp.net", "image", "shared needle", 100)
	first.Caption = "caption needle"
	addSearchMessage(t, s, first)
	addSearchMessage(t, s, searchMessage("7@lid", "second", "2@lid", "text", "shared needle", 90))
	addSearchMessage(t, s, searchMessage("other@g.us", "other", "9@lid", "text", "shared needle", 80))
	if _, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "700@s.whatsapp.net", TargetID: "first", Kind: "revoke", EventID: "r", Timestamp: 101}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "7@lid", TargetID: "second", Kind: "edit", EventID: "e", NewText: "changed", Timestamp: 102}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(ctx, store.SearchParams{Query: "needle", ChatJID: "7@lid", SenderJID: "2@lid", From: 90, To: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "first" || hits[1].ID != "second" {
		t.Fatalf("canonical filtered hits = %+v", hits)
	}
	if hits[0].CanonicalJID != "7@lid" || hits[0].ChatName != "Family" || hits[0].SenderName != "Layla" || !hits[0].Deleted || hits[0].Edited {
		t.Errorf("first hit metadata = %+v", hits[0])
	}
	if !hits[1].Edited || hits[1].Deleted {
		t.Errorf("second hit badges = %+v", hits[1])
	}
	media, err := s.Search(ctx, store.SearchParams{Query: "needle", MediaType: "image"})
	if err != nil || len(media) != 1 || media[0].ID != "first" {
		t.Fatalf("media filter = %+v, %v", media, err)
	}
	page, err := s.Search(ctx, store.SearchParams{Query: "needle", Limit: 1})
	if err != nil || len(page) != 1 || page[0].ID != "first" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	next, err := s.Search(ctx, store.SearchParams{Query: "needle", Limit: 2, BeforeTS: page[0].TS, BeforeChat: page[0].CanonicalJID, BeforeID: page[0].ID})
	if err != nil || len(next) != 2 || next[0].ID != "second" || next[1].ID != "other" {
		t.Fatalf("next page = %+v, %v", next, err)
	}
}

func TestSearchSyntheticPerformanceUnder300Milliseconds(t *testing.T) {
	if testing.Short() {
		t.Skip("scale timing")
	}
	s := open(t)
	for i := range 5_000 {
		text := "ordinary archive text"
		if i%41 == 0 {
			text = "search performance marker"
		}
		addSearchMessage(t, s, searchMessage(fmt.Sprintf("chat-%02d@g.us", i%50), fmt.Sprintf("m-%05d", i), fmt.Sprintf("sender-%02d@lid", i%20), "text", text, int64(i+1)))
	}
	start := time.Now()
	hits, err := s.Search(t.Context(), store.SearchParams{Query: "performance", Limit: 50})
	elapsed := time.Since(start)
	if err != nil || len(hits) != 50 {
		t.Fatalf("scale Search = %d hits, %v", len(hits), err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("scale Search took %s, want under 300ms", elapsed)
	}
	t.Logf("5k-message search: %s", elapsed)
}
