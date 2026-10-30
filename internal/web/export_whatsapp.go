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

var waStart = regexp.MustCompile(`^\[(\d{2}/\d{2}/\d{4}, \d{2}:\d{2}:\d{2})\] ([^:]*): (.*)$`)

func mTime(ts int64, loc *time.Location) string {
	return time.Unix(ts, 0).In(loc).Format("02/01/2006, 15:04:05")
}

func writeWALine(w io.Writer, at time.Time, sender, body string) error {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\n\u200e")
	_, err := fmt.Fprintf(w, "[%s] %s: %s\n", at.Format("02/01/2006, 15:04:05"), strings.ReplaceAll(sender, "\n", " "), body)
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
func parseWhatsAppText(r io.Reader, _ *time.Location) ([]waLine, error) {
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
		t, err := time.Parse("02/01/2006, 15:04:05", m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, waLine{t, m[2], m[3]})
	}
	return out, s.Err()
}
