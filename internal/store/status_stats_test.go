package store_test

import (
	"context"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

// W2(f): StatusReport and ChatList read chat_stats, not the raw tables. Changing the table behind the store's back shows
// which one is read (a stored value no raw row can produce).
func TestStatusReportAndChatListReadChatStats(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	apply(t, s, withChat(mb("5@s.whatsapp.net", "A", "text", "x", 10), "Layla", "individual"))
	if _, err := rawDB(t, s).Exec(`UPDATE chat_stats SET messages=42, last_ts=99, oldest_ts=7, oldest_history_ts=123, media_failed=1, media_expired=2, media_pending=3, gaps_open=4`); err != nil {
		t.Fatal(err)
	}
	r, err := s.StatusReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := store.ChatStat{CanonicalJID: "5@s.whatsapp.net", Name: "Layla", MessageCount: 42, Oldest: 7, Newest: 99, OldestHistory: 123, MediaFailed: 1, MediaExpired: 2, MediaPending: 3, OpenGaps: 4}
	if len(r.Chats) != 1 || r.Chats[0] != want || r.Coverage.OldestByChat["5@s.whatsapp.net"] != 123 {
		t.Fatalf("StatusReport = %+v, want the stored row %+v and oldest history 123", r, want)
	}
	rows, err := s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChatStat != want {
		t.Fatalf("ChatList = %+v, want the stored row %+v", rows, want)
	}
}

// The status report lists chats with messages only (as before); the chat list also lists the chats known from a rule, a
// sighting or the directory, with zero counts.
func TestStatusReportListsChatsWithMessagesOnlyChatListAlsoTheQuietOnes(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	apply(t, s, mb("5@s.whatsapp.net", "A", "text", "x", 10))
	if err := s.SetChatRules(ctx, []store.ChatRule{{JID: "6@s.whatsapp.net", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(ctx, store.Chat{JID: "g@g.us", Name: "Quiet group", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.StatusReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Chats) != 1 || r.Chats[0].CanonicalJID != "5@s.whatsapp.net" {
		t.Fatalf("StatusReport chats = %+v, want only the chat with a message", r.Chats)
	}
	rows, err := s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].CanonicalJID != "5@s.whatsapp.net" {
		t.Fatalf("ChatList = %+v, want the chat with messages first, then the two quiet ones", rows)
	}
	if q := rowFor(t, rows, "6@s.whatsapp.net"); q.Logging || q.MessageCount != 0 {
		t.Errorf("rule-only row %+v", q)
	}
	if q := rowFor(t, rows, "g@g.us"); q.DisplayName != "Quiet group" || q.MessageCount != 0 {
		t.Errorf("directory-only row %+v", q)
	}
}

// W2(e) left the readers on the push name; the order is the chat's name, then the contact name, then the push name
// (nameExpr), in ChatName, the chat list, the status report and the sender names of a chat page.
func TestNamesFollowTheChatNameContactNamePushNameOrder(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	const dm = "5@s.whatsapp.net"
	apply(t, s, mb(dm, "A", "text", "x", 10))
	if err := s.UpsertParticipant(ctx, store.Participant{JID: dm, PushName: "lay", UpdatedAt: 11}); err != nil {
		t.Fatal(err)
	}
	names := func() (chatName, display, status string) {
		t.Helper()
		chatName, err := s.ChatName(ctx, dm)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.ChatList(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.StatusReport(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return chatName, rowFor(t, rows, dm).DisplayName, r.Chats[0].Name
	}
	check := func(step, want string) {
		t.Helper()
		if a, b, c := names(); a != want || b != want || c != want {
			t.Errorf("%s: ChatName=%q ChatList=%q StatusReport=%q, all want %q", step, a, b, c, want)
		}
	}
	check("push name only", "lay")
	setContacts(t, s, store.ContactName{JID: dm, Name: "Layla Khan"})
	check("contact name beats push name", "Layla Khan")
	apply(t, s, store.Batch{Chat: &store.Chat{JID: dm, Name: "Layla (work)"}})
	check("chat name beats contact name", "Layla (work)")
}

func TestChatPageSenderNamePrefersTheContactName(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	apply(t, s, mb("g@g.us", "A", "text", "from alice", 10))
	apply(t, s, mb("g@g.us", "B", "text", "from bob", 20, func(m *store.Message) { m.SenderJID = "222@lid" }))
	if err := s.UpsertParticipant(ctx, store.Participant{JID: "222@lid", PushName: "Bob", UpdatedAt: 20}); err != nil {
		t.Fatal(err)
	}
	setContacts(t, s, store.ContactName{JID: alice, Name: "Alice Karim"})
	page, err := s.ChatPage(ctx, "g@g.us", 0, "", 10)
	if err != nil || len(page) != 2 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if page[0].ID != "B" || page[0].SenderName != "Bob" || page[1].ID != "A" || page[1].SenderName != "Alice Karim" {
		t.Fatalf("sender names = %q for B and %q for A, want Bob (push name) and Alice Karim (contact name beats push name Alice)", page[0].SenderName, page[1].SenderName)
	}
}

// A person known under two LIDs and a phone number is one chat (chat_canonical, R8). Every read that takes "the chat's
// JID forms" follows chat_canonical, so the canonical JID shown in the list reaches all of its messages, and a name
// stored under any form is found from any other.
func TestChatViewsFollowTheWholeCanonicalGroup(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mapping(t, s, "1@lid", "9@s.whatsapp.net")
	mapping(t, s, "2@lid", "9@s.whatsapp.net")
	apply(t, s, mb("1@lid", "A", "text", "under the first lid", 10))
	apply(t, s, withMedia(mb("2@lid", "B", "image", "", 20), "done", 5, ""))
	apply(t, s, mb("9@s.whatsapp.net", "C", "text", "under the phone", 30))
	apply(t, s, ev("2@lid", "B", "reaction", "R1", 25, func(e *store.MessageEvent) { e.Value = "+" }))
	apply(t, s, store.Batch{Chat: &store.Chat{JID: "1@lid", Name: "Nine"}})

	rows, err := s.ChatList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CanonicalJID != "1@lid" || rows[0].MessageCount != 3 {
		t.Fatalf("ChatList = %+v, want one chat 1@lid with 3 messages", rows)
	}
	page, err := s.ChatPage(ctx, "1@lid", 0, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range page {
		ids = append(ids, m.ID)
	}
	if len(ids) != 3 || ids[0] != "C" || ids[1] != "B" || ids[2] != "A" {
		t.Fatalf("ChatPage of the canonical JID = %v, want C, B, A (all three forms)", ids)
	}
	if page[1].MediaState != "done" || len(page[1].Reactions) != 1 {
		t.Errorf("message B = %+v, want its media state and reaction from the other LID", page[1])
	}
	for _, form := range []string{"1@lid", "2@lid", "9@s.whatsapp.net"} {
		name, err := s.ChatName(ctx, form)
		if err != nil || name != "Nine" {
			t.Errorf("ChatName(%s) = %q, %v, want Nine", form, name, err)
		}
		tr, err := s.Trace(ctx, form, "B")
		if err != nil || tr.Message.ID != "B" || len(tr.Events) != 1 || tr.Media == nil {
			t.Errorf("Trace(%s, B) = %+v, %v", form, tr, err)
		}
		m, err := s.ChatMedia(ctx, form, "B")
		if err != nil || m == nil {
			t.Errorf("ChatMedia(%s, B) = %v, %v", form, m, err)
		}
	}
}

// Usage groups the media sizes by canonical chat: the forms of a chat are one line.
func TestUsageGroupsByCanonicalChat(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mapping(t, s, "1@lid", "9@s.whatsapp.net")
	for _, m := range []store.Media{
		doneMedia("1@lid", "1", "image", 100, usageNow),
		doneMedia("9@s.whatsapp.net", "2", "image", 50, usageNow),
		doneMedia("g@g.us", "3", "video", 7, usageNow),
	} {
		if err := testkit.InsertMedia(ctx, s, m); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Usage(ctx, usageNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.ByChat) != 2 || u.ByChat[0] != (store.ChatBytes{ChatJID: "1@lid", Bytes: 150}) || u.ByChat[1] != (store.ChatBytes{ChatJID: "g@g.us", Bytes: 7}) {
		t.Fatalf("ByChat = %+v, want 1@lid 150 (both forms) then g@g.us 7", u.ByChat)
	}
}
