package store_test

import (
	"fmt"
	"testing"

	"whatsapp-archiver/internal/store"
)

func TestDenseShortWordSearchKeepsUnicodeTokenSemantics(t *testing.T) {
	s := open(t)
	for i := range 1_010 {
		addSearchMessage(t, s, searchMessage("g@g.us", fmt.Sprintf("exact-%04d", i), "1@lid", "text", "أنا في البيت", int64(i+1)))
	}
	addSearchMessage(t, s, searchMessage("g@g.us", "substring", "1@lid", "text", "هذه سفينة كبيرة", 9_999))
	hits, err := s.Search(t.Context(), store.SearchParams{Query: "في", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.ID == "substring" {
			t.Fatalf("short word search substring-matched longer token: %+v", hits)
		}
	}
	if len(hits) != 50 {
		t.Fatalf("dense exact short-word hits = %d, want 50", len(hits))
	}
}
