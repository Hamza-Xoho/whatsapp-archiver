package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SearchParams selects one page of archived message matches. ChatJID and SenderJID may be any known alias; results
// always carry canonical chat identifiers. From and To are inclusive unix seconds. The cursor follows the result
// order (newest first). Limit is capped at 50.
type SearchParams struct {
	Query                         string
	ChatJID, SenderJID, MediaType string
	From, To, BeforeTS            int64
	BeforeChat, BeforeID          string
	Limit                         int
}

// SearchHit is one message match. Text is the original text or caption; edits and deletes are labelled separately.
type SearchHit struct {
	CanonicalJID, ChatName string
	ID, Sender, SenderName string
	Text, Kind             string
	TS                     int64
	Deleted, Edited        bool
}

// searchMatch folds a user query, safely phrase-quotes every whitespace-delimited term, and chooses the tokenizer
// from schema R18. A quote inside a phrase is represented by a doubled quote in FTS5 syntax.
func searchMatch(query string) (table, match string) {
	terms := strings.Fields(Fold(query))
	if len(terms) == 0 {
		return "", ""
	}
	table = "search_tri"
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		if utf8.RuneCountInString(term) < 3 {
			table = "search_fts"
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return table, strings.Join(quoted, " ")
}

// Search finds positive-rowid FTS hits (messages; edit rows use negative rowids), resolves aliases, deduplicates a
// message copied under two aliases, applies filters, and returns newest-first keyset results.
func (s *Store) Search(ctx context.Context, p SearchParams) ([]SearchHit, error) {
	table, match := searchMatch(p.Query)
	if match == "" {
		return nil, nil
	}
	limit := p.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	where := []string{"rk=1"}
	args := []any{match}
	add := func(clause string, values ...any) {
		where = append(where, clause)
		args = append(args, values...)
	}
	if p.ChatJID != "" {
		add(`canonical=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?),?)`, p.ChatJID, p.ChatJID)
	}
	if p.SenderJID != "" {
		add(`sender_canonical=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?),?)`, p.SenderJID, p.SenderJID)
	}
	if p.From > 0 {
		add(`ts>=?`, p.From)
	}
	if p.To > 0 {
		add(`ts<=?`, p.To)
	}
	if p.MediaType != "" {
		add(`kind=?`, p.MediaType)
	}
	if p.BeforeTS > 0 {
		add(`(ts<? OR (ts=? AND (canonical>? OR (canonical=? AND id>?))))`, p.BeforeTS, p.BeforeTS, p.BeforeChat, p.BeforeChat, p.BeforeID)
	}
	args = append(args, limit)
	query := `WITH hits AS (
 SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ?
), copies AS (
 SELECT c.canonical,m.id,m.sender_jid,
  COALESCE((SELECT canonical FROM chat_canonical WHERE alias=m.sender_jid),m.sender_jid) AS sender_canonical,
  CASE WHEN m.text<>'' THEN m.text ELSE m.caption END AS body,m.kind,m.ts,m.received_at,m.chat_jid,
  ROW_NUMBER() OVER (PARTITION BY c.canonical,m.id ORDER BY m.received_at,m.chat_jid) AS rk
 FROM hits h JOIN messages m ON m.seq=h.rowid JOIN chat_canonical c ON c.alias=m.chat_jid
 WHERE h.rowid>0
), picked AS (
 SELECT * FROM copies WHERE ` + strings.Join(where, " AND ") + `
)
SELECT p.canonical,` + nameExpr("p.canonical") + `,p.id,p.sender_jid,` + nameOfJID("p.sender_jid") + `,p.body,p.kind,p.ts,
 EXISTS(SELECT 1 FROM message_events e JOIN chat_canonical a ON a.alias=e.chat_jid
        WHERE a.canonical=p.canonical AND e.target_id=p.id AND e.kind='revoke'),
 EXISTS(SELECT 1 FROM message_events e JOIN chat_canonical a ON a.alias=e.chat_jid
        WHERE a.canonical=p.canonical AND e.target_id=p.id AND e.kind='edit')
FROM picked p ORDER BY p.ts DESC,p.canonical,p.id LIMIT ?`
	var out []SearchHit
	err := s.scanRows(ctx, query, args, func(rows *sql.Rows) error {
		var h SearchHit
		if err := rows.Scan(&h.CanonicalJID, &h.ChatName, &h.ID, &h.Sender, &h.SenderName, &h.Text, &h.Kind, &h.TS, &h.Deleted, &h.Edited); err != nil {
			return err
		}
		if h.ChatName == "" {
			h.ChatName = h.CanonicalJID
		}
		if h.SenderName == "" {
			h.SenderName = h.Sender
		}
		out = append(out, h)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search archive: %w", err)
	}
	return out, nil
}
