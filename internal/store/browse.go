package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Reads behind the chats screen, the chat viewer and the message trace. All of them use the read pool.

// ChatRow is one line of the chats screen. Logging, HasMediaRule and New take every JID form of the chat into account.
type ChatRow struct {
	ChatStat
	DisplayName  string
	Logging      bool
	HasMediaRule bool
	New          bool
}

// ViewMessage is one message as the chat viewer shows it. Text is the text as first received; EditedText is the
// latest edit, if any. Reactions are each reactor's current reaction (a removed reaction is not listed).
type ViewMessage struct {
	ID, Sender, SenderName, Text, EditedText, MediaType, MediaState string
	TS                                                              int64
	Deleted                                                         bool
	Reactions                                                       []string
	QuotedID                                                        string
}

// Trace is everything the archive holds about one message. Message is zero when only events arrived; Gap is true
// when one of them says the message could not be decrypted.
type Trace struct {
	Message Message
	Events  []MessageEvent
	Media   *Media
	Gap     bool
}

// canonicalOf is the canonical JID of the column or parameter X, as chat_aliases defines it (the LID of a mapped
// phone number or LID, else X itself), answered from id_mappings lookups instead of building the view.
func canonicalOf(x string) string {
	return `COALESCE((SELECT im.lid FROM id_mappings im WHERE im.lid=` + x + ` OR im.pn=` + x + ` ORDER BY im.lid LIMIT 1),` + x + `)`
}

// ChatList is the chats screen: every chat with messages, newest activity first, then every chat known only from
// chat_rules, chat_seen or chats (no stored message: zero counts), each once under its canonical JID. DisplayName is the chat's name (a
// group's subject), else the participant's name for a direct chat, else the JID. filter keeps the chats whose name or
// JID contains it, ignoring case.
func (s *Store) ChatList(ctx context.Context, filter string) ([]ChatRow, error) {
	report, err := s.StatusReport(ctx)
	if err != nil {
		return nil, fmt.Errorf("chat list: %w", err)
	}
	people, err := s.stringsByCanonical(ctx, `SELECT `+canonicalOf("p.jid")+`,MAX(p.display_name) FROM participants p WHERE p.display_name<>'' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	type ruleAgg struct{ logging, media bool }
	rules := map[string]ruleAgg{}
	err = s.scanRows(ctx, `SELECT `+canonicalOf("r.jid")+`,MIN(r.logging),MAX(r.media<>'') FROM chat_rules r GROUP BY 1`, nil, func(rows *sql.Rows) error {
		var jid string
		var a ruleAgg
		if err := rows.Scan(&jid, &a.logging, &a.media); err != nil {
			return err
		}
		rules[jid] = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A chat is new when it was seen under some JID form and no form of it has been acknowledged.
	fresh := map[string]bool{}
	err = s.scanRows(ctx, `SELECT `+canonicalOf("c.jid")+`,MAX(c.acknowledged) FROM chat_seen c GROUP BY 1`, nil, func(rows *sql.Rows) error {
		var jid string
		var acked bool
		if err := rows.Scan(&jid, &acked); err != nil {
			return err
		}
		fresh[jid] = !acked
		return nil
	})
	if err != nil {
		return nil, err
	}
	chats, err := s.stringsByCanonical(ctx, `SELECT `+canonicalOf("jid")+`,MAX(name) FROM (SELECT jid,'' AS name FROM chat_rules UNION ALL SELECT jid,'' FROM chat_seen UNION ALL SELECT jid,name FROM chats) GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(report.Chats))
	for _, c := range report.Chats {
		listed[c.CanonicalJID] = true
	}
	quiet := make([]string, 0, len(chats))
	for jid := range chats {
		if !listed[jid] {
			quiet = append(quiet, jid)
		}
	}
	sort.Strings(quiet)
	for _, jid := range quiet {
		report.Chats = append(report.Chats, ChatStat{CanonicalJID: jid, Name: chats[jid]})
	}
	needle := strings.ToLower(filter)
	out := make([]ChatRow, 0, len(report.Chats))
	for _, c := range report.Chats {
		row := ChatRow{ChatStat: c, DisplayName: c.Name, Logging: true, New: fresh[c.CanonicalJID]}
		if row.DisplayName == "" {
			row.DisplayName = people[c.CanonicalJID]
		}
		if row.DisplayName == "" {
			row.DisplayName = c.CanonicalJID
		}
		if r, ok := rules[c.CanonicalJID]; ok {
			row.Logging, row.HasMediaRule = r.logging, r.media
		}
		if needle != "" && !strings.Contains(strings.ToLower(row.DisplayName), needle) && !strings.Contains(strings.ToLower(c.CanonicalJID), needle) {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Newest > out[j].Newest })
	return out, nil
}

// ChatName is the chat's name, else the participant's name for a direct chat, over all its JID forms; "" when the
// archive knows neither.
func (s *Store) ChatName(ctx context.Context, jid string) (string, error) {
	var name string
	err := s.read.QueryRowContext(ctx, `SELECT COALESCE((SELECT name FROM chats WHERE jid IN (`+aliasSet+`) AND name<>'' ORDER BY jid LIMIT 1),
 (SELECT display_name FROM participants WHERE jid IN (`+aliasSet+`) AND display_name<>'' ORDER BY jid LIMIT 1),'')`, jid).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("chat name of %s: %w", jid, err)
	}
	return name, nil
}

// chatPageQuery is messages_resolved for one chat: its messages under every JID form (aliasSet of ?1), each id once
// (the first received copy), newest first, at most ?3, and, when ?2 > 0, after the cursor (?2, ?4) in that order:
// older than ?2, or at ?2 with an id below ?4. The dedupe rank is computed over all the chat's rows before the cursor
// filter. Restricting to the chat's forms before the window function keeps the cost to the chat's own rows instead of
// the whole archive.
const chatPageQuery = `WITH m AS (
 SELECT chat_jid,id,sender_jid,ts,text,quoted_id,media_type,
  ROW_NUMBER() OVER (PARTITION BY id ORDER BY received_at,chat_jid) AS rk
 FROM messages WHERE chat_jid IN (` + aliasSet + `)
)
SELECT m.id,m.sender_jid,COALESCE(p.display_name,''),m.text,m.media_type,m.ts,m.quoted_id
FROM m LEFT JOIN participants p ON p.jid=m.sender_jid
WHERE m.rk=1 AND (?2<=0 OR m.ts<?2 OR (m.ts=?2 AND m.id<?4))
ORDER BY m.ts DESC,m.id DESC LIMIT ?3`

// ChatPage is one page of the viewer: at most limit messages of the chat after the cursor (beforeTS, beforeID) in
// newest-first order (all when beforeTS is 0), newest first, with their edits, deletion, reactions and media state.
func (s *Store) ChatPage(ctx context.Context, canonicalJID string, beforeTS int64, beforeID string, limit int) ([]ViewMessage, error) {
	var page []ViewMessage
	err := s.scanRows(ctx, chatPageQuery, []any{canonicalJID, beforeTS, limit, beforeID}, func(rows *sql.Rows) error {
		var m ViewMessage
		if err := rows.Scan(&m.ID, &m.Sender, &m.SenderName, &m.Text, &m.MediaType, &m.TS, &m.QuotedID); err != nil {
			return err
		}
		page = append(page, m)
		return nil
	})
	if err != nil || len(page) == 0 {
		return page, err
	}
	at := make(map[string]int, len(page))
	args := []any{canonicalJID}
	marks := make([]string, 0, len(page))
	for i, m := range page {
		at[m.ID] = i
		args = append(args, m.ID)
		marks = append(marks, fmt.Sprintf("?%d", i+2))
	}
	in := strings.Join(marks, ",")
	reactions := map[string]map[string]string{} // message id -> reactor -> current reaction
	err = s.scanRows(ctx, `SELECT target_id,kind,sender_jid,new_text,value FROM message_events WHERE chat_jid IN (`+aliasSet+`) AND kind IN ('edit','revoke','reaction') AND target_id IN (`+in+`) ORDER BY ts,received_at,event_id`, args, func(rows *sql.Rows) error {
		var id, kind, sender, newText, value string
		if err := rows.Scan(&id, &kind, &sender, &newText, &value); err != nil {
			return err
		}
		m := &page[at[id]]
		switch kind {
		case "edit":
			m.EditedText = newText
		case "revoke":
			m.Deleted = true
		case "reaction":
			if reactions[id] == nil {
				reactions[id] = map[string]string{}
			}
			reactions[id][sender] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for id, by := range reactions {
		reactors := make([]string, 0, len(by))
		for r := range by {
			reactors = append(reactors, r)
		}
		sort.Strings(reactors)
		for _, r := range reactors {
			if by[r] != "" {
				page[at[id]].Reactions = append(page[at[id]].Reactions, by[r])
			}
		}
	}
	err = s.scanRows(ctx, `SELECT message_id,state FROM media WHERE chat_jid IN (`+aliasSet+`) AND message_id IN (`+in+`) ORDER BY chat_jid`, args, func(rows *sql.Rows) error {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return err
		}
		if m := &page[at[id]]; m.MediaState == "" {
			m.MediaState = state
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// Trace reads message id of the chat (under any of its JID forms), every event about it, and its media row. It
// returns sql.ErrNoRows when the archive holds none of them.
func (s *Store) Trace(ctx context.Context, chatJID, id string) (Trace, error) {
	var t Trace
	m := &t.Message
	err := s.read.QueryRowContext(ctx, `SELECT chat_jid,id,sender_jid,ts,received_at,source,text,quoted_id,forwarded,media_type,raw_proto FROM messages WHERE id=?2 AND chat_jid IN (`+aliasSet+`) ORDER BY received_at,chat_jid LIMIT 1`, chatJID, id).
		Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.Timestamp, &m.ReceivedAt, &m.Source, &m.Text, &m.QuotedID, &m.Forwarded, &m.MediaType, &m.RawProto)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("trace message %s: %w", id, err)
	}
	err = s.scanRows(ctx, `SELECT chat_jid,target_id,kind,event_id,sender_jid,new_text,value,ts,received_at FROM message_events WHERE target_id=?2 AND chat_jid IN (`+aliasSet+`) ORDER BY ts,received_at,event_id`, []any{chatJID, id}, func(rows *sql.Rows) error {
		var e MessageEvent
		if err := rows.Scan(&e.ChatJID, &e.TargetID, &e.Kind, &e.EventID, &e.SenderJID, &e.NewText, &e.Value, &e.Timestamp, &e.ReceivedAt); err != nil {
			return err
		}
		t.Events = append(t.Events, e)
		if e.Kind == "undecryptable" && t.Message.ID == "" {
			t.Gap = true
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	if t.Media, err = s.ChatMedia(ctx, chatJID, id); err != nil {
		return t, err
	}
	if t.Message.ID == "" && len(t.Events) == 0 && t.Media == nil {
		return t, fmt.Errorf("trace %s in %s: %w", id, chatJID, sql.ErrNoRows)
	}
	return t, nil
}

// ChatMedia is the media row of message id in the chat under any of its JID forms, or nil.
func (s *Store) ChatMedia(ctx context.Context, chatJID, id string) (*Media, error) {
	var m Media
	err := s.read.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM media WHERE message_id=?2 AND chat_jid IN (`+aliasSet+`) ORDER BY chat_jid LIMIT 1`, chatJID, id).Scan(m.fields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media of %s: %w", id, err)
	}
	return &m, nil
}

// stringsByCanonical runs a two-column (canonical JID, value) query into a map.
func (s *Store) stringsByCanonical(ctx context.Context, query string) (map[string]string, error) {
	out := map[string]string{}
	err := s.scanRows(ctx, query, nil, func(rows *sql.Rows) error {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		out[k] = v
		return nil
	})
	return out, err
}

// scanRows runs query on the read pool and calls each for every row.
func (s *Store) scanRows(ctx context.Context, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("browse query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err = each(rows); err != nil {
			return fmt.Errorf("browse scan: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("browse rows: %w", err)
	}
	return nil
}
