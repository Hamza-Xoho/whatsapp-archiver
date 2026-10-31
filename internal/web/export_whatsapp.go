package web

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
)

type waLine struct {
	Time         time.Time
	Sender, Body string
}

var waStart = regexp.MustCompile(`^\[(\d{2}/\d{2}/\d{4}, \d{2}:\d{2}:\d{2})\] (.*)$`)

func mTime(ts int64, loc *time.Location) string {
	return time.Unix(ts, 0).In(loc).Format("02/01/2006, 15:04:05")
}

func writeWALine(w io.Writer, at time.Time, sender, body string) error {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\n\u200e")
	sender = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(sender, "\\", "\\\\"), ":", "\\:"), "\n", " ")
	var line strings.Builder
	line.Grow(25 + len(sender) + len(body))
	line.WriteByte('[')
	line.WriteString(at.Format("02/01/2006, 15:04:05"))
	line.WriteString("] ")
	line.WriteString(sender)
	line.WriteString(": ")
	line.WriteString(body)
	line.WriteByte('\n')
	_, err := io.WriteString(w, line.String())
	return err
}

func eventLabel(e store.MessageEvent) string {
	switch e.Kind {
	case "edit":
		return "[edited] " + e.NewText
	case "revoke":
		return "[deleted]"
	case "reaction":
		return "[reaction " + e.Value + "]"
	default:
		if e.Value != "" {
			return "[" + e.Kind + "] " + e.Value
		}
		return "[" + e.Kind + "]"
	}
}

func htmlEventLabel(e store.MessageEvent) string {
	label := eventLabel(e)
	if len(label) > 2 {
		label = "[" + strings.ToUpper(label[1:2]) + label[2:]
	}
	return label
}

// parseWhatsAppText is the test oracle for the documented WhatsApp-compatible line format. A left-to-right mark
// distinguishes continuation lines whose user text itself resembles a new timestamped message.
func parseWhatsAppText(r io.Reader, loc *time.Location) ([]waLine, error) {
	var out []waLine
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		m := waStart.FindStringSubmatch(line)
		if m == nil {
			if len(out) == 0 {
				return nil, fmt.Errorf("continuation before message")
			}
			out[len(out)-1].Body += "\n" + strings.TrimPrefix(line, "\u200e")
			continue
		}
		sender, body, ok := splitWASender(m[2])
		if !ok {
			if len(out) == 0 {
				return nil, fmt.Errorf("message has no sender delimiter")
			}
			out[len(out)-1].Body += "\n" + strings.TrimPrefix(line, "\u200e")
			continue
		}
		t, err := time.ParseInLocation("02/01/2006, 15:04:05", m[1], loc)
		if err != nil {
			return nil, err
		}
		out = append(out, waLine{t, sender, body})
	}
	return out, s.Err()
}

func splitWASender(s string) (string, string, bool) {
	escaped := false
	for i := 0; i+1 < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == ':' && s[i+1] == ' ' {
			sender := strings.ReplaceAll(strings.ReplaceAll(s[:i], "\\:", ":"), "\\\\", "\\")
			return sender, s[i+2:], true
		}
	}
	return "", "", false
}
