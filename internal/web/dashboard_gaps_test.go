package web

import (
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestDashboardGroupsOpenGapsByCausePostOutageAndAge(t *testing.T) {
	d := newDash(t)
	postOutageAt := dashNow.Add(-73 * time.Hour)
	if err := d.st.InsertClosedConnectionPeriod(t.Context(), store.ConnectionPeriod{
		State: "reconnecting", StartedAt: postOutageAt.Add(-time.Hour).Unix(), EndedAt: postOutageAt.Add(-time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []store.MessageEvent{
		{ChatJID: "1@g.us", TargetID: "old", Kind: "undecryptable", EventID: "gap-old", Cause: "no_sender_key", ReceivedAt: postOutageAt.Unix()},
		{ChatJID: "1@g.us", TargetID: "recent", Kind: "undecryptable", EventID: "gap-recent", Cause: "view_once_unavailable", ReceivedAt: dashNow.Add(-90 * time.Minute).Unix()},
	} {
		if _, err := d.st.InsertEvent(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}

	got := d.page(t, "/")
	for _, want := range []string{
		"Open gaps by cause",
		"No sender key: 1 gap, oldest 3 days ago, after an outage",
		"View-once unavailable: 1 gap, oldest 1 hour ago",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, got)
		}
	}
}

func TestDashboardOpenGapsHasAnExplicitEmptyState(t *testing.T) {
	got := newDash(t).page(t, "/")
	if !strings.Contains(got, "No open gaps") {
		t.Fatalf("dashboard lacks empty gap copy: %s", got)
	}
}
