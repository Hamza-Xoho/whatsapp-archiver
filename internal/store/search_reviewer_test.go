package store_test

import (
	"fmt"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestSearchBroadCandidatesCannotHideOlderExactPhrase(t *testing.T) {
	s := open(t)
	for i := range 100 {
		addSearchMessage(t, s, searchMessage("g@g.us", fmt.Sprintf("apart-%03d", i), "1@lid", "text", "foo x bar", int64(1_000+i)))
	}
	addSearchMessage(t, s, searchMessage("g@g.us", "exact", "1@lid", "text", "foo bar", 1))

	hits, err := s.Search(t.Context(), store.SearchParams{Query: "foo bar", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "exact" {
		t.Fatalf("Search returned %+v; broad newer candidates must not hide the older exact phrase", hits)
	}
}
