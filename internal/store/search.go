package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"unicode"
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

// searchMatch folds and safely phrase-quotes the whole query. Token lengths choose the tokenizer from schema R18;
// surrounding quote punctuation is syntax, not part of a token's length. Embedded quotes are doubled for FTS5.
func searchMatch(query string) (table, match string) {
	folded := strings.TrimSpace(Fold(query))
	terms := strings.Fields(folded)
	if len(terms) == 0 {
		return "", ""
	}
	table = "search_tri"
	hasTerm := false
	for _, term := range terms {
		term = strings.TrimFunc(term, unicode.IsPunct)
		if term == "" {
			continue
		}
		hasTerm = true
		if utf8.RuneCountInString(term) < 3 {
			table = "search_fts"
		}
	}
	if !hasTerm {
		return "", ""
	}
	return table, `"` + strings.ReplaceAll(folded, `"`, `""`) + `"`
}

// searchCandidateMatch is a safe, broader candidate query. Search still requires the whole normalised phrase below;
// this pass lets punctuation between terms (for example foo "bar") reach that validation with the trigram tokenizer.
func searchCandidateMatch(query string) string {
	terms := strings.Fields(Fold(query))
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		if term = strings.TrimFunc(term, unicode.IsPunct); term != "" {
			quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
	}
	return strings.Join(quoted, " ")
}

func searchPhraseText(text string) string {
	var b strings.Builder
	space := true
	for _, r := range Fold(text) {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(b.String())
}

func searchPhraseMatches(text, query string) bool {
	needle := searchPhraseText(query)
	return needle != "" && strings.Contains(searchPhraseText(text), needle)
}

const newestSearchThreshold = 1000

// denseSearch reports whether at least newestSearchThreshold exact FTS rows exist. The LIMIT makes this bounded for a
// common term; sparse terms keep the FTS-first plan, while dense terms avoid sorting the complete hit set.
func (s *Store) denseSearch(ctx context.Context, table, match string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM (SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ? LIMIT ?)`
	if err := s.read.QueryRowContext(ctx, query, match, newestSearchThreshold).Scan(&count); err != nil {
		return false, fmt.Errorf("count search candidates: %w", err)
	}
	return count == newestSearchThreshold, nil
}

func (s *Store) hasEditSearchHit(ctx context.Context, table, match string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM ` + table + ` WHERE ` + table + ` MATCH ? AND rowid<0 LIMIT 1)`
	if err := s.read.QueryRowContext(ctx, query, match).Scan(&exists); err != nil {
		return false, fmt.Errorf("check edit search candidates: %w", err)
	}
	return exists, nil
}

// searchDenseNewest streams the global message index and stops as soon as the page is full. denseSearch has already
// routed the query through the required FTS table; this bounded path avoids sorting a very large common-term hit set.
func (s *Store) searchDenseNewest(ctx context.Context, p SearchParams, limit int) ([]SearchHit, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT c.canonical,m.id,m.sender_jid,m.text,m.caption,m.details,
 COALESCE(x.filename,''),m.kind,m.ts
FROM messages m INDEXED BY messages_global_ts
JOIN chat_canonical c ON c.alias=m.chat_jid
LEFT JOIN media x ON x.chat_jid=m.chat_jid AND x.message_id=m.id
ORDER BY m.ts DESC,m.seq DESC`)
	if err != nil {
		return nil, fmt.Errorf("search newest messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[string]struct{}, limit)
	out := make([]SearchHit, 0, limit)
	for rows.Next() {
		var h SearchHit
		var text, caption, details, filename string
		if err = rows.Scan(&h.CanonicalJID, &h.ID, &h.Sender, &text, &caption, &details, &filename, &h.Kind, &h.TS); err != nil {
			return nil, err
		}
		h.Text = searchContext(h.Kind, text, caption, details, filename)
		if !searchPhraseMatches(h.Text, p.Query) {
			continue
		}
		key := h.CanonicalJID + "\x00" + h.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, h)
		if len(out) == limit {
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("search newest messages: %w", err)
	}
	if err = s.enrichSearchHits(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
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
	var where []string
	var filterArgs []any
	add := func(clause string, values ...any) {
		where = append(where, clause)
		filterArgs = append(filterArgs, values...)
	}
	if p.ChatJID != "" {
		add(`canonical=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?),?)`, p.ChatJID, p.ChatJID)
	}
	if p.SenderJID != "" {
		add(`COALESCE((SELECT canonical FROM chat_canonical WHERE alias=sender_jid),sender_jid)=COALESCE((SELECT canonical FROM chat_canonical WHERE alias=?),?)`, p.SenderJID, p.SenderJID)
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
	fetchLimit := limit * 2
	if fetchLimit > 100 {
		fetchLimit = 100
	}
	candidateMatch := searchCandidateMatch(p.Query)
	unfiltered := p.ChatJID == "" && p.SenderJID == "" && p.MediaType == "" && p.From == 0 && p.To == 0 && p.BeforeTS == 0
	fastNewest := false
	if unfiltered && table == "search_tri" {
		var err error
		if fastNewest, err = s.denseSearch(ctx, table, match); err != nil {
			return nil, err
		}
	}
	if fastNewest {
		hasEdits, err := s.hasEditSearchHit(ctx, table, match)
		if err != nil {
			return nil, err
		}
		if !hasEdits {
			return s.searchDenseNewest(ctx, p, limit)
		}
	}
	var args []any
	var hitsQuery string
	includeEditCandidates := !fastNewest
	if fastNewest {
		hitsQuery = `SELECT rowid FROM (SELECT m.seq AS rowid FROM messages m INDEXED BY messages_global_ts
 WHERE EXISTS(SELECT 1 FROM ` + table + ` f WHERE f.rowid=m.seq AND ` + table + ` MATCH ?)
 ORDER BY m.ts DESC,m.seq DESC LIMIT ?)`
		args = append(args, match, fetchLimit)
		// Edits have negative FTS rowids and are not in the message time index. Avoid making the dense message plan a
		// compound query when no edit matches; SQLite would otherwise materialize the indexed scan.
		hasEdits, err := s.hasEditSearchHit(ctx, table, match)
		if err != nil {
			return nil, err
		}
		if hasEdits {
			includeEditCandidates = true
			hitsQuery += ` UNION ALL SELECT rowid FROM (SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ? AND rowid<0 LIMIT ?)`
			args = append(args, match, fetchLimit)
		}
		if candidateMatch != match {
			hitsQuery += ` UNION ALL SELECT rowid FROM (SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ? LIMIT ?)`
			args = append(args, candidateMatch, 500)
		}
	} else {
		hitsQuery = `SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ?`
		args = append(args, match)
		if candidateMatch != match {
			hitsQuery += ` UNION ALL SELECT rowid FROM (SELECT rowid FROM ` + table + ` WHERE ` + table + ` MATCH ? LIMIT ?)`
			args = append(args, candidateMatch, 500)
		}
	}
	args = append(args, filterArgs...)
	args = append(args, fetchLimit)
	candidatesQuery := `SELECT c.canonical,m.id,m.sender_jid,
  m.text,m.caption,m.details,
  '' AS edit_text,m.kind,m.ts,m.received_at,m.chat_jid,0 AS is_edit
 FROM hits h JOIN messages m ON m.seq=h.rowid JOIN chat_canonical c ON c.alias=m.chat_jid
 WHERE h.rowid>0`
	if includeEditCandidates {
		candidatesQuery += ` UNION ALL SELECT ec.canonical,m.id,m.sender_jid,
  m.text,m.caption,m.details,
  e.new_text,m.kind,CASE WHEN e.ts>0 THEN e.ts ELSE m.ts END,e.received_at,m.chat_jid,1
 FROM hits h JOIN message_events e ON e.seq=-h.rowid AND e.kind='edit'
 JOIN chat_canonical ec ON ec.alias=e.chat_jid
 JOIN chat_canonical mc ON mc.canonical=ec.canonical
 JOIN messages m ON m.chat_jid=mc.alias AND m.id=e.target_id
	WHERE h.rowid<0`
	}
	query := `WITH hits AS (` + hitsQuery + `), candidates AS (` + candidatesQuery + `), page AS (SELECT * FROM candidates`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY ts DESC,canonical,id,is_edit DESC,received_at DESC,chat_jid LIMIT ?)
SELECT p.canonical,p.id,p.sender_jid,p.text,p.caption,p.details,COALESCE(x.filename,''),
 p.edit_text,p.is_edit,p.kind,p.ts
FROM page p LEFT JOIN media x ON x.chat_jid=p.chat_jid AND x.message_id=p.id
ORDER BY p.ts DESC,p.canonical,p.id,p.is_edit DESC,p.received_at DESC,p.chat_jid`
	var out []SearchHit
	seen := make(map[string]struct{}, limit)
	err := s.scanRows(ctx, query, args, func(rows *sql.Rows) error {
		var h SearchHit
		var text, caption, details, filename, editText string
		var isEdit bool
		if err := rows.Scan(&h.CanonicalJID, &h.ID, &h.Sender, &text, &caption, &details, &filename, &editText, &isEdit, &h.Kind, &h.TS); err != nil {
			return err
		}
		if isEdit {
			h.Text = editText
		} else {
			h.Text = searchContext(h.Kind, text, caption, details, filename)
		}
		h.Edited = isEdit
		if !searchPhraseMatches(h.Text, p.Query) {
			return nil
		}
		key := h.CanonicalJID + "\x00" + h.ID
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		out = append(out, h)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search archive: %w", err)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if err = s.enrichSearchHits(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) enrichSearchHits(ctx context.Context, hits []SearchHit) error {
	if len(hits) == 0 {
		return nil
	}
	var jids []string
	for _, h := range hits {
		if !slices.Contains(jids, h.CanonicalJID) {
			jids = append(jids, h.CanonicalJID)
		}
		if h.Sender != "" && !slices.Contains(jids, h.Sender) {
			jids = append(jids, h.Sender)
		}
	}
	names, err := s.senderNames(ctx, jids)
	if err != nil {
		return fmt.Errorf("search names: %w", err)
	}
	args := make([]any, 0, len(hits)*2)
	marks := make([]string, 0, len(hits))
	for _, h := range hits {
		marks = append(marks, "(?,?)")
		args = append(args, h.CanonicalJID, h.ID)
	}
	badges := make(map[string]map[string]bool, len(hits))
	err = s.scanRows(ctx, `WITH wanted(c,id) AS (VALUES `+strings.Join(marks, ",")+`)
SELECT w.c,w.id,e.kind FROM wanted w JOIN chat_canonical a ON a.canonical=w.c
JOIN message_events e ON e.chat_jid=a.alias AND e.target_id=w.id
WHERE e.kind IN ('edit','revoke') GROUP BY w.c,w.id,e.kind`, args, func(rows *sql.Rows) error {
		var canonical, id, kind string
		if scanErr := rows.Scan(&canonical, &id, &kind); scanErr != nil {
			return scanErr
		}
		key := canonical + "\x00" + id
		if badges[key] == nil {
			badges[key] = map[string]bool{}
		}
		badges[key][kind] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("search badges: %w", err)
	}
	for i := range hits {
		h := &hits[i]
		h.ChatName = names[h.CanonicalJID]
		if h.ChatName == "" {
			h.ChatName = h.CanonicalJID
		}
		h.SenderName = names[h.Sender]
		if h.SenderName == "" {
			h.SenderName = h.Sender
		}
		badge := badges[h.CanonicalJID+"\x00"+h.ID]
		h.Edited = h.Edited || badge["edit"]
		h.Deleted = badge["revoke"]
	}
	return nil
}
