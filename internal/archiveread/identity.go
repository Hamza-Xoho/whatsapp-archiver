package archiveread

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"whatsapp-archiver/internal/store"
)

type identities struct {
	parent    map[string]string
	canonical map[string]string
	groups    map[string][]string
	allowed   map[string]bool
	version   string
	ctx       context.Context
}

func (i *identities) root(s string) string {
	p, ok := i.parent[s]
	if !ok {
		i.parent[s] = s
		return s
	}
	if p != s {
		i.parent[s] = i.root(p)
	}
	return i.parent[s]
}
func (i *identities) resolve(s string) string {
	if c, ok := i.canonical[s]; ok {
		return c
	}
	return s
}
func loadIdentities(ctx context.Context, tx *sql.Tx, policy Policy) (*identities, error) {
	i := &identities{ctx: ctx, parent: map[string]string{}, canonical: map[string]string{}, groups: map[string][]string{}}
	rows, err := tx.QueryContext(ctx, "SELECT "+bytePrefixSQL("lid", 513)+","+bytePrefixSQL("pn", 513)+" FROM id_mappings ORDER BY lid,pn")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if ctx.Err() != nil {
			_ = rows.Close()
			return nil, ctx.Err()
		}
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !utf8.ValidString(a) || !utf8.ValidString(b) || len(a) > 512 || len(b) > 512 || len(i.parent) > 100000 {
			_ = rows.Close()
			return nil, failure("QUERY_TOO_BROAD")
		}
		ra, rb := i.root(a), i.root(b)
		if ra != rb {
			i.parent[ra] = rb
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Seek past each raw identifier using existing leading-column indexes.
	// UNION/DISTINCT still walks every message duplicate, even when the bounded
	// scalar is outside the distinct subquery. Keyset seeks touch only distinct
	// identifiers and apply the NUL-safe prefix after selecting one indexed key.
	for _, source := range [][2]string{{"chats", "jid"}, {"messages", "chat_jid"}, {"message_events", "chat_jid"}} {
		table, column := source[0], source[1]
		last, first := "", true
		for {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			where := ""
			var args []any
			if !first {
				where = " WHERE " + column + ">?"
				args = []any{last}
			}
			query := "SELECT " + bytePrefixSQL(column, 513) + " FROM (SELECT " + column + " FROM " + table + where + " ORDER BY " + column + " LIMIT 1)"
			var s string
			if err = tx.QueryRowContext(ctx, query, args...).Scan(&s); errors.Is(err, sql.ErrNoRows) {
				break
			} else if err != nil {
				return nil, err
			}
			if !utf8.ValidString(s) || len(s) > 512 || len(i.parent) > 100000 {
				return nil, failure("QUERY_TOO_BROAD")
			}
			i.root(s)
			last, first = s, false
		}
	}
	rawGroups := map[string][]string{}
	for s := range i.parent {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		root := i.root(s)
		rawGroups[root] = append(rawGroups[root], s)
	}
	for _, members := range rawGroups {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sort.Strings(members)
		canonical := members[0]
		for _, s := range members {
			if strings.HasSuffix(s, "@lid") {
				canonical = s
				break
			}
		}
		i.groups[canonical] = members
		for _, s := range members {
			i.canonical[s] = canonical
		}
	}
	if err = i.setPolicy(policy); err != nil {
		return nil, err
	}
	return i, nil
}
func (i *identities) setPolicy(p Policy) error {
	i.allowed = map[string]bool{}
	for c := range i.groups {
		if i.ctx.Err() != nil {
			return i.ctx.Err()
		}
		i.allowed[c] = p.Mode == "all"
	}
	for _, s := range p.AllowChats {
		c := i.resolve(s)
		if _, ok := i.groups[c]; ok {
			i.allowed[c] = true
		}
	}
	for _, s := range p.DenyChats {
		i.allowed[i.resolve(s)] = false
	}
	keys := make([]string, 0, len(i.canonical))
	for key := range i.canonical {
		if i.ctx.Err() != nil {
			return i.ctx.Err()
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		if i.ctx.Err() != nil {
			return i.ctx.Err()
		}
		b, _ := json.Marshal([]string{key, i.canonical[key]})
		_, _ = hash.Write(b)
	}
	b, _ := json.Marshal(p)
	_, _ = hash.Write(b)
	i.version = hex.EncodeToString(hash.Sum(nil))
	return i.ctx.Err()
}
func (i *identities) permitted(s string) bool { return i.allowed[i.resolve(s)] }
func (i *identities) permittedAliases() []string {
	a := []string{}
	for c, g := range i.groups {
		if i.ctx.Err() != nil {
			return nil
		}
		if i.allowed[c] {
			a = append(a, g...)
		}
	}
	sort.Strings(a)
	return a
}
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func stringsAny(ss []string) []any {
	out := make([]any, len(ss))
	for j, s := range ss {
		out[j] = s
	}
	return out
}

type Chat struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Clipped []string `json:"clipped"`
}

func (s *snapshot) chatMetadata(ctx context.Context) ([]Chat, error) {
	aliases := s.ids.permittedAliases()
	out := []Chat{}
	if len(aliases) == 0 {
		return out, nil
	}
	if len(aliases) > 10000 {
		return nil, failure("QUERY_TOO_BROAD")
	}
	byID := map[string]Chat{}
	for _, a := range aliases {
		c := s.ids.resolve(a)
		byID[c] = Chat{ID: c, Clipped: []string{}}
	}
	rows, err := s.tx.QueryContext(ctx, "SELECT jid,"+boundedSQL("name", 65536)+","+boundedSQL("type", 64)+" FROM chats WHERE jid IN ("+placeholders(len(aliases))+") ORDER BY jid", stringsAny(aliases)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, n, t string
		var nameCut, typeCut bool
		if err = rows.Scan(&a, &n, &nameCut, &t, &typeCut); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if ctx.Err() != nil {
			_ = rows.Close()
			return nil, ctx.Err()
		}
		n, nameCut, err = finishPrefix(n, nameCut, 65536)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		t, typeCut, err = finishPrefix(t, typeCut, 64)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if nameCut {
			_ = rows.Close()
			return nil, failure("QUERY_TOO_BROAD")
		}
		c := s.ids.resolve(a)
		ch := byID[c]
		if ch.Name == "" {
			ch.Name = n
		}
		if ch.Type == "" {
			ch.Type = t
			if typeCut {
				ch.Clipped = appendClip(ch.Clipped, "type")
			}
		}
		byID[c] = ch
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for _, ch := range byID {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (e *Engine) listChats(ctx context.Context, s *snapshot, a arguments, r *Result) error {
	chats, err := s.chatMetadata(ctx)
	if err != nil {
		return err
	}
	hash := filterHash("list_chats", a)
	c, err := e.decodeCursor(a.Cursor, s, hash)
	if err != nil {
		return err
	}
	limit := a.limit(20)
	out := []Chat{}
	last := ""
	more := false
	for _, ch := range chats {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ch.ID <= c.Last.ID || !strings.Contains(store.Fold(ch.Name+" "+ch.ID), store.Fold(a.Query)) {
			continue
		}
		if len(out) >= limit {
			more = true
			break
		}
		var clipped bool
		ch.Name, clipped = clip(ch.Name, 512)
		if clipped {
			ch.Clipped = append(ch.Clipped, "name")
		}
		ch.Type, clipped = clip(ch.Type, 64)
		if clipped {
			ch.Clipped = append(ch.Clipped, "type")
		}
		candidate := append(out, ch)
		r.Data = map[string]any{"chats": candidate}
		b, _ := json.Marshal(r)
		if len(b) > responseBudget-2048 {
			if len(out) == 0 {
				return failure("QUERY_TOO_BROAD")
			}
			more = true
			break
		}
		out = candidate
		last = ch.ID
	}
	r.Data = map[string]any{"chats": out}
	r.HasMore = more
	if more {
		r.NextCursor = e.encodeCursor(s, hash, orderKey{ID: last}, 0, 0)
	}
	return nil
}
