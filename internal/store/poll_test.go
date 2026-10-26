package store_test

import (
	"slices"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func pollRow(chat, id, details string) store.Batch {
	return store.Batch{Message: &store.Message{ChatJID: chat, ID: id, SenderJID: "3@lid", Kind: "poll", Text: "Lunch?", Details: details, Source: "live", Timestamp: 10, ReceivedAt: 10, RawProto: []byte("p")}}
}

func TestPollOptionsReadsTheOptionsOfAStoredPoll(t *testing.T) {
	st := testkit.Store(t)
	if _, err := st.Apply(t.Context(), pollRow("g@g.us", "POLL", `{"question":"Lunch?","options":["Yes","No"],"max":1}`)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.PollOptions(t.Context(), "g@g.us", "POLL")
	if err != nil || !ok || !slices.Equal(got, []string{"Yes", "No"}) {
		t.Fatalf("options=%v ok=%v err=%v", got, ok, err)
	}
}

func TestPollOptionsOfAnUnknownOrNonPollMessageIsNotFound(t *testing.T) {
	st := testkit.Store(t)
	if _, err := st.Apply(t.Context(), store.Batch{Message: &store.Message{ChatJID: "g@g.us", ID: "TXT", Kind: "text", Source: "live", Timestamp: 10, ReceivedAt: 10, RawProto: []byte("t")}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TXT", "NOPE"} {
		if got, ok, err := st.PollOptions(t.Context(), "g@g.us", id); err != nil || ok || got != nil {
			t.Fatalf("%s: options=%v ok=%v err=%v", id, got, ok, err)
		}
	}
}

// A poll stored under the phone-number form of a chat is found from its LID form (CON-7: resolved at read time).
func TestPollOptionsFollowsTheChatAliases(t *testing.T) {
	st := testkit.Store(t)
	if _, err := st.InsertIDMapping(t.Context(), store.IDMapping{LID: "9@lid", PN: "9@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(t.Context(), pollRow("9@s.whatsapp.net", "POLL", `{"options":["A","B"]}`)); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := st.PollOptions(t.Context(), "9@lid", "POLL"); err != nil || !ok || !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("options=%v ok=%v err=%v", got, ok, err)
	}
}
