package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/testkit"
)

func (u *chatsUI) mediaRow(t *testing.T, chat, id, state, mime, path string) {
	t.Helper()
	if err := testkit.InsertMedia(t.Context(), u.st, store.Media{ChatJID: chat, MessageID: id, Type: "image", MIME: mime, State: state, Path: path}); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func getMedia(t *testing.T, h http.Handler, chat, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/media?"+url.Values{"chat": {chat}, "id": {id}}.Encode(), nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Review Focus: only a done media row's own file under <data>/media is served.
func TestMediaServesOnlyDoneRowFileInsideMediaDir(t *testing.T) {
	u := newChatsUI(t)
	data := u.cfg.DataDir
	good := filepath.Join(data, "media", "g_g.us", "2026-10", "OK.jpg")
	writeFile(t, good, "JPEGDATA")
	pending := filepath.Join(data, "media", "g_g.us", "2026-10", "WAIT.jpg")
	writeFile(t, pending, "PENDING")
	secret := filepath.Join(data, "session.db")
	writeFile(t, secret, "SECRET")
	u.mediaRow(t, "g@g.us", "OK", "done", "image/jpeg", good)
	u.mediaRow(t, "g@g.us", "WAIT", "pending", "image/jpeg", pending)
	u.mediaRow(t, "g@g.us", "OUT", "done", "image/jpeg", secret)
	u.mediaRow(t, "g@g.us", "../../session.db", "done", "image/jpeg", filepath.Join(data, "media", "..", "session.db"))
	h := u.handler()

	for _, c := range []struct{ name, chat, id string }{
		{"pending row", "g@g.us", "WAIT"},
		{"done row outside the media folder", "g@g.us", "OUT"},
		{"traversal in id", "g@g.us", "../../session.db"},
		{"traversal with no row", "g@g.us", "../../../session.db"},
		{"unknown row", "g@g.us", "NOPE"},
		{"traversal in chat", "../..", "OK"},
	} {
		if w := getMedia(t, h, c.chat, c.id); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 (body %q)", c.name, w.Code, w.Body.String())
		}
	}

	w := getMedia(t, h, "g@g.us", "OK")
	if w.Code != http.StatusOK || w.Body.String() != "JPEGDATA" {
		t.Fatalf("valid row: status %d body %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type %q, want image/jpeg", ct)
	}
	if v := w.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", v)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `inline; filename="OK.jpg"` {
		t.Errorf("Content-Disposition %q", cd)
	}
}

// A file under the media folder reached through a symbolic link that points outside it is not served.
func TestMediaRefusesSymlinkOutOfMediaDir(t *testing.T) {
	u := newChatsUI(t)
	data := u.cfg.DataDir
	secret := filepath.Join(data, "session.db")
	writeFile(t, secret, "SECRET")
	link := filepath.Join(data, "media", "g_g.us", "2026-10", "L.jpg")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	u.mediaRow(t, "g@g.us", "L", "done", "image/jpeg", link)
	if w := getMedia(t, u.handler(), "g@g.us", "L"); w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

// SVG and HTML can run script when opened inline, so they are always downloads.
func TestMediaServesActiveTypesAsAttachment(t *testing.T) {
	u := newChatsUI(t)
	data := u.cfg.DataDir
	for id, mime := range map[string]string{"S": "image/svg+xml", "H": "text/html", "X": "Image/SVG+XML; charset=utf-8"} {
		p := filepath.Join(data, "media", "g_g.us", "2026-10", id+".bin")
		writeFile(t, p, "<svg onload=alert(1)>")
		u.mediaRow(t, "g@g.us", id, "done", mime, p)
		w := getMedia(t, u.handler(), "g@g.us", id)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", mime, w.Code)
		}
		if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="`+id+`.bin"` {
			t.Errorf("%s: Content-Disposition %q, want attachment", mime, cd)
		}
	}
}

// A media row under the chat's other JID form is found from the canonical chat the viewer links with.
func TestMediaFoundUnderAlias(t *testing.T) {
	u := newChatsUI(t)
	if _, err := u.st.InsertIDMapping(t.Context(), store.IDMapping{LID: "77@lid", PN: "9665@s.whatsapp.net", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(u.cfg.DataDir, "media", "9665_s.whatsapp.net", "2026-10", "A.jpg")
	writeFile(t, p, "IMG")
	u.mediaRow(t, "9665@s.whatsapp.net", "A", "done", "image/jpeg", p)
	if w := getMedia(t, u.handler(), "77@lid", "A"); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

// A message whose media is downloaded shows the picture in the viewer, through the media endpoint.
func TestViewerLinksDoneMedia(t *testing.T) {
	u := newChatsUI(t)
	u.msg(t, "g@g.us", "OK", "1@lid", 1760000000, "")
	p := filepath.Join(u.cfg.DataDir, "media", "g_g.us", "2026-10", "OK.jpg")
	writeFile(t, p, "JPEGDATA")
	u.mediaRow(t, "g@g.us", "OK", "done", "image/jpeg", p)
	body, _, _ := get(t, u.handler(), "/chats/g@g.us")
	if want := `/media?chat=g%40g.us&amp;id=OK`; !strings.Contains(string(body), want) {
		t.Fatalf("viewer has no media link %s: %s", want, body)
	}
}
