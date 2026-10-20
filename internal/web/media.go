package web

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// unsafeName is what a download file name may not hold; message ids come from other people's phones.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// media is GET /media?chat=&id=: the downloaded file of one message. Only a done media row's own file, inside
// <data>/media after resolving symbolic links, is served; anything else is 404. The file is labelled with its stored
// MIME type and never sniffed; only pictures, audio and video open inline, everything else (SVG and HTML above all,
// which could run script) is a download.
func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	chat, id := q.Get("chat"), q.Get("id")
	if chat == "" || id == "" || s.d.Store == nil {
		http.NotFound(w, r)
		return
	}
	m, err := s.d.Store.ChatMedia(r.Context(), chat, id)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	if m == nil || m.State != "done" {
		http.NotFound(w, r)
		return
	}
	path, ok := s.insideMediaDir(m.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ctype, inline := mediaType(m.MIME)
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	name := unsafeName.ReplaceAllString(m.MessageID, "_")
	if ext := filepath.Ext(m.Path); ext != "" {
		name += "." + unsafeName.ReplaceAllString(ext[1:], "_")
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, name))
	h.Set("Content-Security-Policy", "sandbox; frame-ancestors 'none'")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// mediaType is the Content-Type to send for a stored MIME type, and whether the file may open inline.
func mediaType(stored string) (ctype string, inline bool) {
	t, _, err := mime.ParseMediaType(stored)
	if err != nil || !strings.Contains(t, "/") {
		return "application/octet-stream", false
	}
	switch {
	case t == "image/svg+xml":
		return t, false
	case strings.HasPrefix(t, "image/"), strings.HasPrefix(t, "audio/"), strings.HasPrefix(t, "video/"):
		return t, true
	}
	return t, false
}

// insideMediaDir resolves p and reports whether the file it names lies inside <data>/media.
func (s *Server) insideMediaDir(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(s.config().DataDir, "media"))
	if err != nil {
		return "", false
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", false
	}
	rel, err := filepath.Rel(dir, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return real, true
}
