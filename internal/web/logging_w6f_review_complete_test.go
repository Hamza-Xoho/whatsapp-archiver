package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func TestLoggingOutageSummaryUsesCompleteLedgerBeyondTimeline(t *testing.T) {
	d := newDash(t)
	for i := range 201 {
		end := dashNow.Add(-time.Duration(i*2) * time.Minute)
		if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{
			State: "disconnected", CauseClass: "tcp", StartedAt: end.Add(-time.Minute).Unix(), EndedAt: end.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	oldEnd := dashNow.AddDate(0, 0, -10)
	if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{
		State: "disconnected", CauseClass: "tcp", StartedAt: oldEnd.Add(-3 * time.Hour).Unix(), EndedAt: oldEnd.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	text := d.page(t, "/logging/outages")
	for _, want := range []string{"Today: 201 minutes", "Longest: 3 hours", "2026-10-20: 201 minutes"} {
		if !strings.Contains(text, want) {
			t.Errorf("complete-ledger outage summary lacks %q: %s", want, text)
		}
	}
}

func TestLoggingGapGroupsUseAllOpenGapsBeyondTraceWindow(t *testing.T) {
	d := newDash(t)
	d.message(t, "family@g.us", "known")
	for i := range 201 {
		id := fmt.Sprintf("missing-%03d", i)
		if _, err := d.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: id,
			Kind: "undecryptable", EventID: "gap-" + id, Cause: "no_sender_key", ReceivedAt: dashNow.Add(-time.Duration(i) * time.Second).Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: "family@g.us", TargetID: "older-untrusted",
		Kind: "undecryptable", EventID: "gap-untrusted", Cause: "untrusted_identity", ReceivedAt: dashNow.Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}

	body := d.page(t, "/logging/gaps-media")
	for _, want := range []string{"No sender key", "201 gaps", "Untrusted identity", "1 gap", "older-untrusted"} {
		if !strings.Contains(body, want) {
			t.Errorf("complete-ledger gap groups lack %q: %s", want, body)
		}
	}
}

func TestLoggingHistoryUsesAuthoritativeFullCoverage(t *testing.T) {
	d := newDash(t)
	for _, chunk := range []store.HistoryChunk{
		{ID: "initial", Stage: store.StageReceived, SyncType: "INITIAL_BOOTSTRAP", Progress: 100, ReceivedAt: dashNow.Add(-2 * time.Hour).Unix()},
		{ID: "full", Stage: store.StageReceived, SyncType: "FULL", Progress: 75, ReceivedAt: dashNow.Add(-time.Hour).Unix()},
	} {
		if err := d.st.InsertHistoryChunk(t.Context(), chunk); err != nil {
			t.Fatal(err)
		}
	}
	history := testkit.Message("history@g.us", "history-1", dashNow.Add(-30*24*time.Hour))
	history.Source = "history"
	if _, err := d.st.InsertMessage(t.Context(), history); err != nil {
		t.Fatal(err)
	}

	text := d.page(t, "/logging/history")
	if !strings.Contains(text, "History received back to") || strings.Contains(text, "Complete — history received back to") || strings.Contains(text, "History complete back to") {
		t.Fatalf("incomplete FULL was overridden by completed INITIAL_BOOTSTRAP: %s", text)
	}
}

func TestLoggingHistoryChatListHasTotalAndKeysetContinuation(t *testing.T) {
	d := newDash(t)
	for i := range 251 {
		chat := fmt.Sprintf("chat-%03d@g.us", i)
		message := testkit.Message(chat, "history-1", dashNow.Add(-time.Duration(i+1)*time.Hour))
		message.Source = "history"
		if _, err := d.st.InsertMessage(t.Context(), message); err != nil {
			t.Fatal(err)
		}
	}

	body, code, _ := get(t, d.handler(), "/logging/history")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	page := string(body)
	for _, want := range []string{"Showing 250 of 251 chats", `rel="next"`, `/logging/history?after=chat-249%40g.us`} {
		if !strings.Contains(page, want) {
			t.Errorf("bounded history page lacks %q", want)
		}
	}
	if body2, code2, _ := get(t, d.handler(), "/logging/history?after=chat-249%40g.us"); code2 != 200 || !strings.Contains(string(body2), "chat-250@g.us") {
		t.Fatalf("history continuation status=%d body=%s", code2, body2)
	}
}
