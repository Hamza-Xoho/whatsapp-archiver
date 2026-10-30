package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ViewerQuery is one bounded, keyset-based chat read. Times are inclusive filters.
type ViewerQuery struct {
	BeforeTS, AfterTS int64
	BeforeID, AfterID string
	Sender, MediaKind string
	FromTS, ToTS      int64
	FocusID           string
	Limit             int
}

// ViewerAnchor returns the stored ordering key of id under any form of a canonical chat.
func (s *Store) ViewerAnchor(ctx context.Context, chat, id string) (int64, string, error) {
	var ts int64
	var got string
	err := s.read.QueryRowContext(ctx, `SELECT ts,id FROM messages WHERE chat_jid IN (`+chatForms+`) AND id=?2 ORDER BY received_at,chat_jid LIMIT 1`, chat, id).Scan(&ts, &got)
	if err != nil {
		return 0, "", fmt.Errorf("viewer anchor %s: %w", id, err)
	}
	return ts, got, nil
}

// ViewerDateAnchor returns the first archived message on or after ts, or the newest older message.
func (s *Store) ViewerDateAnchor(ctx context.Context, chat string, ts int64) (int64, string, error) {
	var gotTS int64
	var id string
	err := s.read.QueryRowContext(ctx, `SELECT ts,id FROM messages WHERE chat_jid IN (`+chatForms+`) AND ts>=?2 ORDER BY ts,id LIMIT 1`, chat, ts).Scan(&gotTS, &id)
	if err == nil {
		return gotTS, id, nil
	}
	if err != sql.ErrNoRows {
		return 0, "", fmt.Errorf("viewer date anchor: %w", err)
	}
	err = s.read.QueryRowContext(ctx, `SELECT ts,id FROM messages WHERE chat_jid IN (`+chatForms+`) ORDER BY ts DESC,id DESC LIMIT 1`, chat).Scan(&gotTS, &id)
	if err != nil {
		return 0, "", fmt.Errorf("viewer date anchor: %w", err)
	}
	return gotTS, id, nil
}

// ViewerLiveCursor is the last inserted message sequence under any form of chat. Unlike the viewer's chronological
// keyset, this cursor cannot miss a newly inserted live message whose WhatsApp timestamp ties or predates the page.
func (s *Store) ViewerLiveCursor(ctx context.Context, chat string) (int64, error) {
	var seq int64
	if err := s.read.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE chat_jid IN (`+chatForms+`)`, chat).Scan(&seq); err != nil {
		return 0, fmt.Errorf("viewer live cursor: %w", err)
	}
	return seq, nil
}

// SearchChat returns matching message ids newest-first. Both original messages and latest edit text are indexed.
func (s *Store) SearchChat(ctx context.Context, chat, query string, filter ViewerQuery) ([]string, error) {
	terms := strings.Fields(Fold(query))
	if len(terms) == 0 {
		return nil, nil
	}
	table := "search_tri"
	for _, term := range terms {
		if len([]rune(term)) < 3 {
			table = "search_fts"
			break
		}
	}
	for i := range terms {
		terms[i] = `"` + strings.ReplaceAll(terms[i], `"`, `""`) + `"`
	}
	match := strings.Join(terms, " AND ")
	args := []any{chat, match}
	where := []string{"m.chat_jid IN (" + chatForms + ")"}
	add := func(column string, value any) {
		args = append(args, value)
		where = append(where, column+fmt.Sprintf("=?%d", len(args)))
	}
	if filter.Sender != "" {
		add("m.sender_jid", filter.Sender)
	}
	if filter.MediaKind != "" {
		add("m.kind", filter.MediaKind)
	}
	if filter.FromTS > 0 {
		args = append(args, filter.FromTS)
		where = append(where, fmt.Sprintf("m.ts>=?%d", len(args)))
	}
	if filter.ToTS > 0 {
		args = append(args, filter.ToTS)
		where = append(where, fmt.Sprintf("m.ts<=?%d", len(args)))
	}
	querySQL := `WITH hits(rowid) AS (SELECT rowid FROM ` + table + ` WHERE body MATCH ?2), found(id) AS (
	 SELECT m.id FROM hits h JOIN messages m ON h.rowid=m.seq WHERE m.chat_jid IN (` + chatForms + `)
	 UNION SELECT e.target_id FROM hits h JOIN message_events e ON h.rowid=-e.seq WHERE e.kind='edit' AND e.chat_jid IN (` + chatForms + `))
	 SELECT m.id,MAX(m.ts) FROM found f JOIN messages m ON m.id=f.id WHERE ` + strings.Join(where, " AND ") + ` GROUP BY m.id ORDER BY MAX(m.ts) DESC,m.id DESC`
	var ids []string
	err := s.scanRows(ctx, querySQL, args, func(rows *sql.Rows) error {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search chat: %w", err)
	}
	return ids, nil
}

// ChatSearchPosition is one bounded point in the newest-first matches for a chat.
// Only the selected and adjacent IDs leave SQLite, even when the match set is large.
type ChatSearchPosition struct {
	Count                         int
	Index                         int
	CurrentID, PreviousID, NextID string
}

// SearchChatPosition returns a selected match and its adjacent matches without
// retaining the complete result set in Go memory.
func (s *Store) SearchChatPosition(ctx context.Context, chat, query string, filter ViewerQuery, index int) (ChatSearchPosition, error) {
	position := ChatSearchPosition{Index: index}
	if index < 0 {
		return position, fmt.Errorf("search chat position: index is out of range")
	}
	terms := strings.Fields(Fold(query))
	if len(terms) == 0 {
		return position, nil
	}
	table := "search_tri"
	for _, term := range terms {
		if len([]rune(term)) < 3 {
			table = "search_fts"
			break
		}
	}
	for i := range terms {
		terms[i] = `"` + strings.ReplaceAll(terms[i], `"`, `""`) + `"`
	}
	args := []any{chat, strings.Join(terms, " AND ")}
	where := []string{"m.chat_jid IN (" + chatForms + ")"}
	add := func(column string, value any) {
		args = append(args, value)
		where = append(where, column+fmt.Sprintf("=?%d", len(args)))
	}
	if filter.Sender != "" {
		add("m.sender_jid", filter.Sender)
	}
	if filter.MediaKind != "" {
		add("m.kind", filter.MediaKind)
	}
	if filter.FromTS > 0 {
		add("m.ts>=", filter.FromTS)
	}
	if filter.ToTS > 0 {
		add("m.ts<=", filter.ToTS)
	}
	args = append(args, index, index-1, index+1)
	currentParam, previousParam, nextParam := len(args)-2, len(args)-1, len(args)
	querySQL := `WITH hits(rowid) AS (SELECT rowid FROM ` + table + ` WHERE body MATCH ?2), found(id) AS (
	 SELECT m.id FROM hits h JOIN messages m ON h.rowid=m.seq WHERE m.chat_jid IN (` + chatForms + `)
	 UNION SELECT e.target_id FROM hits h JOIN message_events e ON h.rowid=-e.seq WHERE e.kind='edit' AND e.chat_jid IN (` + chatForms + `)),
	 ordered(id,ts) AS (SELECT m.id,MAX(m.ts) FROM found f JOIN messages m ON m.id=f.id WHERE ` + strings.Join(where, " AND ") + ` GROUP BY m.id),
	 ranked(id,idx,total) AS (SELECT id,ROW_NUMBER() OVER (ORDER BY ts DESC,id DESC)-1,COUNT(*) OVER () FROM ordered)
	 SELECT COALESCE(MAX(total),0),COALESCE(MAX(CASE WHEN idx=?` + fmt.Sprint(currentParam) + ` THEN id END),''),
	 COALESCE(MAX(CASE WHEN idx=?` + fmt.Sprint(previousParam) + ` THEN id END),''),COALESCE(MAX(CASE WHEN idx=?` + fmt.Sprint(nextParam) + ` THEN id END),'') FROM ranked`
	if err := s.read.QueryRowContext(ctx, querySQL, args...).Scan(&position.Count, &position.CurrentID, &position.PreviousID, &position.NextID); err != nil {
		return position, fmt.Errorf("search chat position: %w", err)
	}
	if position.Count > 0 && position.CurrentID == "" {
		return position, fmt.Errorf("search chat position: index is out of range")
	}
	return position, nil
}

// ViewerPage reads one page plus all sender, event, quote and media facts in bounded batch queries.
func (s *Store) ViewerPage(ctx context.Context, chat string, q ViewerQuery) ([]ViewMessage, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	hasDuplicates, err := s.viewerHasDuplicates(ctx, chat)
	if err != nil {
		return nil, err
	}
	args := []any{chat}
	where := []string{"m.chat_jid IN (" + chatForms + ")"}
	source := "messages m"
	columns := "m.seq,m.id,m.sender_jid,m.from_me,m.kind,m.text,m.caption,m.details,m.quoted_id,m.quoted_sender,m.view_once,m.ephemeral_secs,m.ts"
	if hasDuplicates {
		where[0] = "m.rk=1"
		source = `(SELECT seq,id,sender_jid,kind,ts,
		 ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) AS rk
		 FROM messages WHERE chat_jid IN (` + chatForms + `)) m JOIN messages body ON body.seq=m.seq`
		columns = "body.seq,body.id,body.sender_jid,body.from_me,body.kind,body.text,body.caption,body.details,body.quoted_id,body.quoted_sender,body.view_once,body.ephemeral_secs,body.ts"
	}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q.FocusID != "" {
		ts, id, err := s.ViewerAnchor(ctx, chat, q.FocusID)
		if err != nil {
			return nil, err
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(m.ts<?%d OR (m.ts=?%d AND m.id<=?%d))", len(args)-1, len(args)-1, len(args)))
	} else if q.BeforeTS > 0 {
		args = append(args, q.BeforeTS, q.BeforeID)
		where = append(where, fmt.Sprintf("(m.ts<?%d OR (m.ts=?%d AND m.id<?%d))", len(args)-1, len(args)-1, len(args)))
	}
	if q.AfterTS > 0 {
		args = append(args, q.AfterTS, q.AfterID)
		where = append(where, fmt.Sprintf("(m.ts>?%d OR (m.ts=?%d AND m.id>?%d))", len(args)-1, len(args)-1, len(args)))
	}
	if q.Sender != "" {
		add("m.sender_jid=?%d", q.Sender)
	}
	if q.MediaKind != "" {
		add("m.kind=?%d", q.MediaKind)
	}
	if q.FromTS > 0 {
		add("m.ts>=?%d", q.FromTS)
	}
	if q.ToTS > 0 {
		add("m.ts<=?%d", q.ToTS)
	}
	args = append(args, q.Limit)
	order := "DESC"
	if q.AfterTS > 0 {
		order = "ASC"
	}
	query := `SELECT ` + columns + `
	 FROM ` + source + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY m.ts ` + order + `,m.id ` + order + ` LIMIT ?` + fmt.Sprint(len(args))
	var page []ViewMessage
	err = s.scanRows(ctx, query, args, func(rows *sql.Rows) error {
		var m ViewMessage
		if err := rows.Scan(&m.Seq, &m.ID, &m.Sender, &m.FromMe, &m.Kind, &m.Text, &m.Caption, &m.Details, &m.QuotedID, &m.QuotedSender, &m.ViewOnce, &m.EphemeralSecs, &m.TS); err != nil {
			return err
		}
		if slices.Contains([]string{"image", "video", "gif", "audio", "voice", "document", "sticker"}, m.Kind) {
			m.MediaType = m.Kind
		}
		page = append(page, m)
		return nil
	})
	if err != nil || len(page) == 0 {
		return page, err
	}
	if q.AfterTS > 0 {
		slices.Reverse(page)
	}
	if err = s.enrichViewerPage(ctx, chat, page); err != nil {
		return nil, err
	}
	return page, nil
}

// ViewerLiveAfter returns newly inserted live messages in insertion order. History and replay rows can carry old
// timestamps and belong on an older chronological page, so they do not append at the live edge.
func (s *Store) ViewerLiveAfter(ctx context.Context, chat string, after int64, limit int) ([]ViewMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	hasDuplicates, err := s.viewerHasDuplicates(ctx, chat)
	if err != nil {
		return nil, err
	}
	source, unique := "messages m", "m.chat_jid IN ("+chatForms+")"
	columns := "m.seq,m.id,m.sender_jid,m.from_me,m.kind,m.text,m.caption,m.details,m.quoted_id,m.quoted_sender,m.view_once,m.ephemeral_secs,m.ts"
	if hasDuplicates {
		unique = "m.rk=1"
		source = `(SELECT seq,id,ts,source,
		 ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) AS rk
		 FROM messages WHERE chat_jid IN (` + chatForms + `)) m JOIN messages body ON body.seq=m.seq`
		columns = "body.seq,body.id,body.sender_jid,body.from_me,body.kind,body.text,body.caption,body.details,body.quoted_id,body.quoted_sender,body.view_once,body.ephemeral_secs,body.ts"
	}
	query := `SELECT ` + columns + `
	 FROM ` + source + ` WHERE ` + unique + ` AND m.seq>?2 AND m.source='live' ORDER BY m.seq LIMIT ?3`
	var page []ViewMessage
	err = s.scanRows(ctx, query, []any{chat, after, limit}, func(rows *sql.Rows) error {
		var m ViewMessage
		if err := rows.Scan(&m.Seq, &m.ID, &m.Sender, &m.FromMe, &m.Kind, &m.Text, &m.Caption, &m.Details, &m.QuotedID, &m.QuotedSender, &m.ViewOnce, &m.EphemeralSecs, &m.TS); err != nil {
			return err
		}
		if slices.Contains([]string{"image", "video", "gif", "audio", "voice", "document", "sticker"}, m.Kind) {
			m.MediaType = m.Kind
		}
		page = append(page, m)
		return nil
	})
	if err != nil || len(page) == 0 {
		return page, err
	}
	if err = s.enrichViewerPage(ctx, chat, page); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Store) enrichViewerPage(ctx context.Context, chat string, page []ViewMessage) error {
	at := make(map[string]int, len(page))
	args := []any{chat}
	marks := make([]string, len(page))
	var people []string
	for i := range page {
		at[page[i].ID] = i
		args = append(args, page[i].ID)
		marks[i] = fmt.Sprintf("?%d", i+2)
		for _, jid := range []string{page[i].Sender, page[i].QuotedSender} {
			if jid != "" && !slices.Contains(people, jid) {
				people = append(people, jid)
			}
		}
	}
	names, err := s.senderNames(ctx, people)
	if err != nil {
		return err
	}
	for i := range page {
		page[i].SenderName = names[page[i].Sender]
		page[i].QuotedSenderName = names[page[i].QuotedSender]
	}
	in := strings.Join(marks, ",")
	if err = s.enrichEvents(ctx, chat, in, args, page, at); err != nil {
		return err
	}
	if err = s.enrichMedia(ctx, chat, in, args, page, at); err != nil {
		return err
	}
	return s.enrichQuotes(ctx, chat, page)
}

func (s *Store) enrichEvents(ctx context.Context, chat, in string, args []any, page []ViewMessage, at map[string]int) error {
	reactions := map[string]map[string]string{}
	votes := map[string]map[string][]string{}
	incomplete := map[string]bool{}
	err := s.scanRows(ctx, `SELECT target_id,kind,event_id,sender_jid,new_text,value FROM message_events WHERE chat_jid IN (`+chatForms+`) AND kind IN ('edit','revoke','reaction','poll_vote') AND target_id IN (`+in+`) ORDER BY ts,received_at,event_id`, args, func(rows *sql.Rows) error {
		var id, kind, eventID, sender, text, value string
		if err := rows.Scan(&id, &kind, &eventID, &sender, &text, &value); err != nil {
			return err
		}
		m := &page[at[id]]
		switch kind {
		case "edit":
			m.EditedText = text
		case "revoke":
			m.Deleted = true
		case "reaction":
			if reactions[id] == nil {
				reactions[id] = map[string]string{}
			}
			reactions[id][sender] = value
		case "poll_vote":
			if votes[id] == nil {
				votes[id] = map[string][]string{}
			}
			if sender == "" {
				sender = eventID
			}
			var selected struct {
				Options []string `json:"options"`
			}
			if value == "" || json.Unmarshal([]byte(value), &selected) != nil {
				incomplete[id] = true
			} else {
				votes[id][sender] = selected.Options
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for id, bySender := range reactions {
		var reactors []string
		counts := map[string]int{}
		for sender, emoji := range bySender {
			reactors = append(reactors, sender)
			if emoji != "" {
				counts[emoji]++
			}
		}
		sort.Strings(reactors)
		for _, sender := range reactors {
			if emoji := bySender[sender]; emoji != "" {
				page[at[id]].Reactions = append(page[at[id]].Reactions, emoji)
			}
		}
		var emojis []string
		for emoji := range counts {
			emojis = append(emojis, emoji)
		}
		sort.Strings(emojis)
		for _, emoji := range emojis {
			page[at[id]].ReactionCounts = append(page[at[id]].ReactionCounts, ReactionCount{Emoji: emoji, Count: counts[emoji]})
		}
	}
	for id, bySender := range votes {
		counts := map[string]int{}
		for _, options := range bySender {
			for _, option := range options {
				counts[option]++
			}
		}
		var details PollDetails
		_ = json.Unmarshal([]byte(page[at[id]].Details), &details)
		for _, option := range details.Options {
			page[at[id]].PollOptions = append(page[at[id]].PollOptions, PollOption{Text: option, Votes: counts[option]})
		}
		page[at[id]].PollVoteTotal = len(bySender)
		page[at[id]].PollIncomplete = incomplete[id]
	}
	return nil
}

func (s *Store) enrichMedia(ctx context.Context, chat, in string, args []any, page []ViewMessage, at map[string]int) error {
	return s.scanRows(ctx, `SELECT `+mediaColumns+`,length(thumb)>0 FROM media WHERE chat_jid IN (`+chatForms+`) AND message_id IN (`+in+`) ORDER BY chat_jid`, args, func(rows *sql.Rows) error {
		var media Media
		fields := media.fields()
		var hasThumbnail bool
		if err := rows.Scan(append(fields, &hasThumbnail)...); err != nil {
			return err
		}
		m := &page[at[media.MessageID]]
		if m.Media == nil {
			m.Media, m.MediaState = &media, media.State
			m.HasThumbnail = hasThumbnail
		}
		return nil
	})
}

func (s *Store) enrichQuotes(ctx context.Context, chat string, page []ViewMessage) error {
	args := []any{chat}
	var marks []string
	seen := map[string]bool{}
	for i := range page {
		id := page[i].QuotedID
		if id != "" && !seen[id] {
			seen[id] = true
			args = append(args, id)
			marks = append(marks, fmt.Sprintf("?%d", len(args)))
		}
	}
	if len(marks) == 0 {
		return nil
	}
	quotes := map[string]string{}
	err := s.scanRows(ctx, `WITH q AS (SELECT id,text,caption,ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) AS rk FROM messages WHERE chat_jid IN (`+chatForms+`) AND id IN (`+strings.Join(marks, ",")+`)) SELECT id,text,caption FROM q WHERE rk=1`, args, func(rows *sql.Rows) error {
		var id, text, caption string
		if err := rows.Scan(&id, &text, &caption); err != nil {
			return err
		}
		if text == "" {
			text = caption
		}
		quotes[id] = text
		return nil
	})
	if err != nil {
		return err
	}
	for i := range page {
		page[i].QuotedText, page[i].QuotedArchived = quotes[page[i].QuotedID]
	}
	return nil
}
