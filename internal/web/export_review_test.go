package web

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/store"
)

func TestWhatsAppFormatterTrueRoundTripWithTimezoneAndSenderPunctuation(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 1, 30, 0, 0, loc)
	sender := "Ops: West"
	body := "English\n[01/01/2020, 00:00:00] fake: line\nمرحبا بالعالم"
	var b bytes.Buffer
	if err = writeWALine(&b, at, sender, body); err != nil {
		t.Fatal(err)
	}
	got, err := parseWhatsAppText(&b, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Time.Equal(at) || got[0].Sender != sender || got[0].Body != body {
		t.Fatalf("round trip = %+v, want %v %q %q", got, at, sender, body)
	}
}

func TestExportTranscriptGloballyOrdersMessagesAndAllEventTypes(t *testing.T) {
	u, chat := exportUI(t)
	if _, err := u.st.InsertEvent(t.Context(), store.MessageEvent{ChatJID: chat, TargetID: "", Kind: "protocol", EventID: "orphan", Value: "ORPHAN", Timestamp: time.Date(2026, 3, 8, 7, 0, 30, 0, time.UTC).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.InsertChatEvent(t.Context(), store.ChatEvent{ChatJID: chat, Kind: "subject", EventID: "chat", Value: "CHAT-EVENT", Timestamp: time.Date(2026, 3, 8, 7, 0, 45, 0, time.UTC).Unix()}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=txt")
	text := w.Body.String()
	order := []string{"hello", "[edited]", "[deleted]", "[reaction", "ORPHAN", "CHAT-EVENT", "ملف عربي"}
	last := -1
	for _, needle := range order {
		i := strings.Index(text, needle)
		if i <= last {
			t.Fatalf("%q not globally ordered in %s", needle, text)
		}
		last = i
	}
	w = exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=json")
	raw := w.Body.String()
	for _, want := range []string{`"message_events"`, `"chat_events"`, `"orphan"`, `"CHAT-EVENT"`, `"message_events":4`, `"chat_events":1`} {
		if !strings.Contains(raw, want) {
			t.Errorf("JSON lacks %s: %s", want, raw)
		}
	}
}

func TestExportRendersMeaningfulTypedDetails(t *testing.T) {
	u, chat := exportUI(t)
	m := store.Message{ChatJID: chat, ID: "loc", Kind: "location", Details: `{"name":"Café Riyadh","address":"King Road"}`, Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 3, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"txt", "html"} {
		w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format="+f)
		if !strings.Contains(w.Body.String(), "Café Riyadh") || !strings.Contains(w.Body.String(), "King Road") {
			t.Errorf("%s lacks detail context: %s", f, w.Body.String())
		}
	}
}

type cancelOnWrite struct {
	cancel context.CancelFunc
	wrote  bool
}

func (c *cancelOnWrite) Write(p []byte) (int, error) {
	if !c.wrote {
		c.wrote = true
		c.cancel()
	}
	return len(p), nil
}
func TestExportStopsOnCancellation(t *testing.T) {
	u, chat := exportUI(t)
	ctx, cancel := context.WithCancel(context.Background())
	w := &cancelOnWrite{cancel: cancel}
	x := exportScope{jid: chat, loc: time.UTC}
	err := u.handler()
	_ = err
	if got := New(DepsForExportTest(u)).streamText(ctx, w, x); !errors.Is(got, context.Canceled) {
		t.Fatalf("stream cancellation = %v", got)
	}
}

func DepsForExportTest(u *chatsUI) Deps {
	return Deps{Store: u.st, Config: u.cfg, Now: func() time.Time { return dashNow }}
}

func TestHTMLMediaLimitsThumbnailsInlineTagsAndPreciseOmissions(t *testing.T) {
	u, chat := exportUI(t)
	s := New(DepsForExportTest(u))
	dir := filepath.Join(u.cfg.DataDir, "media")
	p := filepath.Join(dir, "tiny.png")
	if err := os.WriteFile(p, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		m    store.Media
		used int64
		want string
	}{{"per-file", store.Media{MessageID: "p", Filename: "tiny.png", Path: p, MIME: "image/png", DeclaredSize: 5, SHA256: "abc"}, 0, "per-file limit"}, {"total", store.Media{MessageID: "p", Filename: "tiny.png", Path: p, MIME: "image/png", DeclaredSize: 5, SHA256: "abc"}, 4, "total limit"}, {"unsafe", store.Media{MessageID: "p", Filename: "x.svg", Path: p, MIME: "image/svg+xml", DeclaredSize: 5, SHA256: "abc"}, 0, "unsafe type"}}
	for _, tc := range cases {
		var b bytes.Buffer
		used := tc.used
		if err := s.htmlMediaWithLimits(&b, chat, &tc.m, &used, htmlEmbedLimits{PerFile: 4, Total: 8}); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{tc.want, tc.m.Filename, tc.m.MIME, "5 bytes", "abc", "Open in live archive"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("%s lacks %q: %s", tc.name, want, b.String())
			}
		}
	}
	var b bytes.Buffer
	used := int64(0)
	m := store.Media{MessageID: "p", Filename: "tiny.png", Path: p, MIME: "image/png", Thumb: []byte("thumb")}
	if err := s.htmlMediaWithLimits(&b, chat, &m, &used, htmlEmbedLimits{PerFile: 10, Total: 20}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<img") || !strings.Contains(b.String(), "data:image/jpeg;base64,") || !strings.Contains(b.String(), "data:image/png;base64,") {
		t.Fatalf("inline image/thumb missing: %s", b.String())
	}
}

func TestZIPDuplicateAvailableNamesMatchTranscriptAndManifestStreams(t *testing.T) {
	u, chat := exportUI(t)
	dir := filepath.Join(u.cfg.DataDir, "media")
	p := filepath.Join(dir, "report2.txt")
	if err := os.WriteFile(p, []byte("SECOND"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := store.Message{ChatJID: chat, ID: "c", Kind: "document", Caption: "second", Source: "live", Timestamp: time.Now().Unix(), ReceivedAt: 3, RawProto: []byte("x")}
	if _, err := u.st.InsertMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := u.st.Apply(t.Context(), store.Batch{Media: &store.Media{ChatJID: chat, MessageID: "c", Type: "document", MIME: "text/plain", Filename: "report.txt", Path: p, State: "done"}}); err != nil {
		t.Fatal(err)
	}
	w := exportGet(t, u.handler(), "/chats/"+chat+"/export/download?format=zip")
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		_ = r.Close()
		names[f.Name] = string(b)
	}
	if _, ok := names["report.txt"]; !ok {
		t.Fatalf("first media absent: %v", names)
	}
	if _, ok := names["report_2.txt"]; !ok {
		t.Fatalf("duplicate media absent: %v", names)
	}
	text := names["_chat.txt"]
	if !strings.Contains(text, "report.txt") || !strings.Contains(text, "report_2.txt") {
		t.Fatalf("transcript names do not match ZIP: %s", text)
	}
	if _, ok := names["missing-media.txt"]; !ok {
		t.Fatalf("manifest must always stream: %v", names)
	}
}

var _ http.Handler
