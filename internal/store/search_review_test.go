package store_test

import (
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestSearchTreatsTheWholeFoldedQueryAsOneSafePhrase(t *testing.T) {
	s := open(t)
	addSearchMessage(t, s, searchMessage("g@g.us", "phrase", "1@lid", "text", "before foo bar after", 30))
	addSearchMessage(t, s, searchMessage("g@g.us", "apart", "1@lid", "text", "foo x bar", 20))
	addSearchMessage(t, s, searchMessage("g@g.us", "quotes", "1@lid", "text", `she said foo "bar" today`, 10))
	addSearchMessage(t, s, searchMessage("g@g.us", "short", "1@lid", "text", "أنا في البيت", 5))

	hits, err := s.Search(t.Context(), store.SearchParams{Query: "foo bar"})
	if err != nil || len(hits) != 2 || hits[0].ID != "phrase" || hits[1].ID != "quotes" {
		t.Fatalf("whole phrase Search = %+v, %v; want contiguous phrase only", hits, err)
	}
	hits, err = s.Search(t.Context(), store.SearchParams{Query: `foo "bar"`})
	if err != nil || len(hits) != 2 {
		t.Fatalf("embedded quote Search = %+v, %v", hits, err)
	}
	hits, err = s.Search(t.Context(), store.SearchParams{Query: `"في"`})
	if err != nil || len(hits) != 1 || hits[0].ID != "short" {
		t.Fatalf("quoted two-rune term Search = %+v, %v; want unicode word-index hit", hits, err)
	}
}

func TestSearchReturnsEditOnlyHitAndDeduplicatesOriginalAndEdit(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	addSearchMessage(t, s, searchMessage("g@g.us", "target", "1@lid", "text", "original shared phrase", 10))
	if _, err := s.InsertEvent(ctx, store.MessageEvent{ChatJID: "g@g.us", TargetID: "target", Kind: "edit", EventID: "edit-1", NewText: "edited-only words and shared phrase", Timestamp: 40, ReceivedAt: 40}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(ctx, store.SearchParams{Query: "edited-only"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("edit-only Search = %+v, %v", hits, err)
	}
	if h := hits[0]; h.ID != "target" || !h.Edited || h.Text != "edited-only words and shared phrase" || h.TS != 40 {
		t.Fatalf("edit-only hit = %+v; want target message with edit context/time", h)
	}
	hits, err = s.Search(ctx, store.SearchParams{Query: "shared phrase"})
	if err != nil || len(hits) != 1 || hits[0].ID != "target" || hits[0].Text != "edited-only words and shared phrase" {
		t.Fatalf("original+edit Search = %+v, %v; want one newest edit hit", hits, err)
	}
}

func TestSearchContextIncludesTheUnfoldedIndexedKindSource(t *testing.T) {
	s := open(t)
	apply(t, s, mb("g@g.us", "poll", "poll", "Lunch?", 10, func(m *store.Message) {
		m.Details = `{"question":"Lunch?","options":["Pizza Supreme","شاورما"]}`
	}))
	apply(t, s, mb("g@g.us", "location", "location", "", 20, func(m *store.Message) {
		m.Details = `{"name":"Café Riyadh","address":"King Fahd Road"}`
	}))
	apply(t, s, mb("g@g.us", "contact", "contact", "Fatima", 30, func(m *store.Message) {
		m.Details = `{"display_name":"Fatima Zahra","vcard":"BEGIN:VCARD"}`
	}))
	apply(t, s, withMedia(mb("g@g.us", "document", "document", "", 40), "done", 10, "Quarterly Report.pdf"))

	for query, source := range map[string]string{
		"supreme": "Pizza Supreme", "riyadh": "Café Riyadh", "zahra": "Fatima Zahra", "quarterly": "Quarterly Report.pdf",
	} {
		hits, err := s.Search(t.Context(), store.SearchParams{Query: query})
		if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Text, source) {
			t.Errorf("Search(%q) = %+v, %v; context must include %q", query, hits, err, source)
		}
	}
}
