package media

import (
	"fmt"
	"slices"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

// rules returns the media rules for a chat: its own when it has them, else the global ones. A failed read is an
// error, never an answer: guessing the global rules would download media of a chat set to download nothing.
func (q *Queue) rules(chatJID string) (config.MediaRules, error) {
	if q.perChat != nil {
		r, err := q.perChat(chatJID)
		if err != nil {
			return config.MediaRules{}, err
		}
		if r != nil {
			return *r, nil
		}
	}
	return q.global()
}

// decide is the download decision of a media row. The rules speak of the types config.MediaTypes lists; a voice note
// (kind voice since V1) follows the audio rules, so nothing a user configured changes.
func decide(m store.Media, r config.MediaRules) (string, string) {
	typ := m.Type
	if typ == "voice" {
		typ = "audio"
	}
	if slices.Contains(r.Skip, typ) {
		return "skipped", "type skipped"
	}
	if typ == "video" && r.VideoMaxMB > 0 && m.DeclaredSize > int64(r.VideoMaxMB)<<20 {
		return "skipped", fmt.Sprintf("over %d MB cap", r.VideoMaxMB)
	}
	if !slices.Contains(r.Download, typ) {
		return "skipped", "type not enabled"
	}
	return "pending", ""
}
