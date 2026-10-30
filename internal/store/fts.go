package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The two contentless FTS tables (R18, spike 8). They are created by 0001_v1.sql and recreated by RebuildDerived from
// these statements; a test pins that the two texts are the same.
const (
	createSearchFTS = `CREATE VIRTUAL TABLE search_fts USING fts5(body, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 0')`
	createSearchTri = `CREATE VIRTUAL TABLE search_tri USING fts5(body, content='', contentless_delete=1, tokenize='trigram')`
)

type searchDetails struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Options     []string `json:"options"`
	DisplayName string   `json:"display_name"`
	Contacts    []struct {
		DisplayName string `json:"display_name"`
	} `json:"contacts"`
}

func searchParts(kind, text, caption, details, filename string) []string {
	parts := []string{text, caption}
	var d searchDetails
	if details != "" && details != "{}" {
		_ = json.Unmarshal([]byte(details), &d)
	}
	switch kind {
	case "poll":
		parts = append(parts, d.Options...)
	case "location":
		parts = append(parts, d.Name, d.Address)
	case "contact", "contacts":
		parts = append(parts, d.DisplayName)
		for _, c := range d.Contacts {
			parts = append(parts, c.DisplayName)
		}
	case "document":
		parts = append(parts, filename)
	}
	return parts
}

// searchBody is the text a message is indexed under (schema 4.2): fold(text + caption + kind strings), where the kind
// strings are the poll options, the location name and address, the contact names and the document filename. It is
// empty when the message has nothing to search for. details is the validated JSON of the message; an unreadable one
// contributes nothing.
func searchBody(kind, text, caption, details, filename string) string {
	var b strings.Builder
	for _, p := range searchParts(kind, text, caption, details, filename) {
		if p = strings.TrimSpace(p); p != "" {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(p)
		}
	}
	return strings.TrimSpace(Fold(b.String()))
}

// searchContext is the original, unfolded source represented by searchBody. It is safe display text, not markup.
func searchContext(kind, text, caption, details, filename string) string {
	var out []string
	for _, p := range searchParts(kind, text, caption, details, filename) {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// indexBody puts a folded body into both FTS tables under rowid; an empty body indexes nothing.
func indexBody(ctx context.Context, tx execer, rowid int64, body string) error {
	if body == "" {
		return nil
	}
	for _, table := range []string{"search_fts", "search_tri"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(rowid, body) VALUES(?,?)`, rowid, body); err != nil {
			return fmt.Errorf("index %d in %s: %w", rowid, table, classify(err))
		}
	}
	return nil
}

// reindexBody replaces the entry of rowid (contentless_delete=1 allows the delete).
func reindexBody(ctx context.Context, tx execer, rowid int64, body string) error {
	for _, table := range []string{"search_fts", "search_tri"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid=?`, rowid); err != nil {
			return fmt.Errorf("unindex %d in %s: %w", rowid, table, classify(err))
		}
	}
	return indexBody(ctx, tx, rowid, body)
}

// indexMessage indexes a new message; filename is its media row's file name when the batch has one.
func (w batch) indexMessage(ctx context.Context, tx execer, seq int64) error {
	m := w.msg
	filename := ""
	if w.media != nil && w.media.ChatJID == m.ChatJID && w.media.MessageID == m.ID {
		filename = w.media.Filename
	}
	return indexBody(ctx, tx, seq, searchBody(m.Kind, m.Text, m.Caption, w.details, filename))
}

// indexEdit indexes the text of a new edit event under -seq (R13). Only edits: other events keep an emoji or a pin type
// in new_text.
func (w batch) indexEdit(ctx context.Context, tx execer, seq int64) error {
	e := w.event
	if e.Kind != "edit" {
		return nil
	}
	return indexBody(ctx, tx, -seq, strings.TrimSpace(Fold(e.NewText)))
}

// reindexForMedia re-indexes the message a stand-alone media row belongs to, when the row names a file: the filename
// is part of a document's searchable text and the row arrived after the message.
func (w batch) reindexForMedia(ctx context.Context, tx *sql.Tx) error {
	m := w.media
	if m.Filename == "" {
		return nil
	}
	var seq int64
	var kind, text, caption, details string
	err := tx.QueryRowContext(ctx, `SELECT seq,kind,text,caption,details FROM messages WHERE chat_jid=? AND id=?`, m.ChatJID, m.MessageID).Scan(&seq, &kind, &text, &caption, &details)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read message of media %s: %w", m.MessageID, err)
	}
	return reindexBody(ctx, tx, seq, searchBody(kind, text, caption, details, m.Filename))
}
