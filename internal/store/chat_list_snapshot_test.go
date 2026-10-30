package store

import (
	"context"
	"slices"
	"testing"
)

func TestChatListViewUsesOneReadSnapshot(t *testing.T) {
	s := openInternal(t)
	ctx := context.Background()
	if err := s.UpsertChat(ctx, Chat{JID: "one@s.whatsapp.net", Name: "One", Type: "direct"}); err != nil {
		t.Fatal(err)
	}
	inserted := false
	view, err := s.chatListView(ctx, ChatListFilter{}, ChatListAfter{}, 50, func() {
		inserted = true
		if writeErr := s.UpsertChat(ctx, Chat{JID: "two@s.whatsapp.net", Name: "Two", Type: "direct"}); writeErr != nil {
			t.Fatal(writeErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("deterministic between-query hook did not run")
	}
	if view.Total != 1 || len(view.Rows) != 1 || view.Rows[0].CanonicalJID != "one@s.whatsapp.net" ||
		!slices.Equal(view.JIDs, []string{"one@s.whatsapp.net"}) {
		t.Fatalf("page/count/membership crossed snapshots: %+v", view)
	}
	all, err := s.ChatListJIDs(ctx, ChatListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(all, []string{"one@s.whatsapp.net", "two@s.whatsapp.net"}) {
		t.Fatalf("hook did not commit entrant after the snapshot: %v", all)
	}
}
