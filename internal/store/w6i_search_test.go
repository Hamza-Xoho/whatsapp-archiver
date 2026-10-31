package store_test

import (
	"fmt"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestW6IChatSearchPositionKeepsOnlyAdjacentIDs(t *testing.T) {
	s := testkit.Store(t)
	for i := 0; i < 200; i++ {
		m := testkit.Message("large@g.us", fmt.Sprintf("m%03d", i), time.Unix(int64(i+1), 0))
		m.Text = "common archive phrase"
		if _, err := s.InsertMessage(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	position, err := s.SearchChatPosition(t.Context(), "large@g.us", "common", store.ViewerQuery{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if position.Count != 200 || position.Index != 100 || position.CurrentID != "m099" || position.PreviousID != "m100" || position.NextID != "m098" {
		t.Fatalf("bounded position = %+v", position)
	}
	if _, err = s.SearchChatPosition(t.Context(), "large@g.us", "common", store.ViewerQuery{}, 200); err == nil {
		t.Fatal("out-of-range position unexpectedly succeeded")
	}
}
