package web

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

func TestViewerSSEBoundsFullyEncodedMultilineEvent(t *testing.T) {
	u := newChatsUI(t)
	u.typedMessage(t, store.Message{ID: "initial", Kind: "text", Text: "initial", Timestamp: 100})
	bus := activity.NewBus(20)
	pipe := ingest.NewPipeline(u.st, ingest.NewFilter(u.rules, func(jid string) ([]string, error) {
		return u.st.Aliases(context.Background(), jid)
	}), nopQueue{}, nopHealth{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithActivity(bus)
	srv := httptest.NewServer(New(Deps{Store: u.st, Rules: u.rules, Config: u.cfg, Activity: bus}).Handler())
	defer srv.Close()

	page, err := srv.Client().Get(srv.URL + "/chats/g@g.us")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(string(raw))
	if len(match) != 2 {
		t.Fatalf("viewer has no SSE path: %s", raw)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+strings.ReplaceAll(match[1], "&amp;", "&"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	pipe.Handle(liveText("g@g.us", "multiline-live", strings.Repeat("line\n", 2300)))
	wire := readSSEWireEvent(t, bufio.NewReader(resp.Body))
	if len(wire) >= maxViewerSSEPayload || !strings.Contains(wire, "event: viewer-refetch\n") {
		t.Fatalf("encoded multiline event bytes/type = %d/%q, want bounded viewer-refetch", len(wire), wire[:min(len(wire), 120)])
	}
}

func readSSEWireEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				result <- "read error: " + err.Error()
				return
			}
			b.WriteString(line)
			if line == "\n" {
				result <- b.String()
				return
			}
		}
	}()
	select {
	case wire := <-result:
		return wire
	case <-time.After(2 * time.Second):
		t.Fatal("viewer SSE event did not arrive")
		return ""
	}
}
