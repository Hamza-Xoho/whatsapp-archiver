package web

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/url"
	"os"
	"strings"

	"whatsapp-archiver/internal/store"
)

type htmlEmbedLimits struct{ PerFile, Total int64 }

var defaultHTMLEmbedLimits = htmlEmbedLimits{PerFile: 10 << 20, Total: 100 << 20}

func (s *Server) htmlMedia(w io.Writer, jid string, m *store.Media, total *int64) error {
	return s.htmlMediaWithLimits(w, jid, m, total, defaultHTMLEmbedLimits)
}

func (s *Server) htmlMediaWithLimits(w io.Writer, jid string, m *store.Media, total *int64, limits htmlEmbedLimits) error {
	if len(m.Thumb) > 0 && *total+int64(len(m.Thumb)) <= limits.Total {
		*total += int64(len(m.Thumb))
		if _, err := fmt.Fprintf(w, "<img class=thumbnail alt=\"Stored thumbnail\" src=\"data:image/jpeg;base64,%s\">", base64.StdEncoding.EncodeToString(m.Thumb)); err != nil {
			return err
		}
	}
	name := safeMediaName(m.Filename, m.MessageID)
	reason := ""
	path, inside := s.insideMediaDir(m.Path)
	var size int64
	if !inside {
		reason = "missing or outside media folder"
	} else if !safeEmbedMIME(m.MIME) {
		reason = "unsafe type"
	} else if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		reason = "missing file"
	} else {
		size = st.Size()
		if *total+size > limits.Total {
			reason = "total limit"
		} else if size > limits.PerFile {
			reason = "per-file limit"
		}
	}
	if reason == "" {
		b, err := os.ReadFile(path)
		if err != nil {
			reason = "read failure"
		} else {
			*total += int64(len(b))
			ctype, _ := mediaType(m.MIME)
			data := "data:" + ctype + ";base64," + base64.StdEncoding.EncodeToString(b)
			var tag string
			switch {
			case strings.HasPrefix(ctype, "image/"):
				tag = fmt.Sprintf("<img alt=%q src=%q>", name, data)
			case strings.HasPrefix(ctype, "audio/"):
				tag = fmt.Sprintf("<audio controls preload=metadata src=%q></audio>", data)
			case strings.HasPrefix(ctype, "video/"):
				tag = fmt.Sprintf("<video controls preload=metadata src=%q></video>", data)
			default:
				tag = fmt.Sprintf("<a download=%q href=%q>%s</a>", name, data, template.HTMLEscapeString(name))
			}
			_, err = fmt.Fprint(w, tag)
			return err
		}
	}
	if size == 0 {
		size = m.DeclaredSize
	}
	link := "/media?chat=" + url.QueryEscape(jid) + "&id=" + url.QueryEscape(m.MessageID)
	sha := m.SHA256
	if sha == "" {
		sha = "unknown"
	}
	_, err := fmt.Fprintf(w, "<p>Media omitted: %s; filename %s; MIME %s; %d bytes; SHA-256 %s. <a href=%q>Open in live archive</a></p>", template.HTMLEscapeString(reason), template.HTMLEscapeString(name), template.HTMLEscapeString(m.MIME), size, template.HTMLEscapeString(sha), link)
	return err
}

func safeEmbedMIME(raw string) bool {
	t, _, err := mime.ParseMediaType(raw)
	return err == nil && t != "text/html" && t != "image/svg+xml" && (strings.HasPrefix(t, "image/") || strings.HasPrefix(t, "audio/") || strings.HasPrefix(t, "video/") || t == "text/plain" || t == "application/pdf")
}
