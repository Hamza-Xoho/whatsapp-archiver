package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"whatsapp-archiver/internal/store"
)

func (s *Server) eachEvent(ctx context.Context, x exportScope, fn func(store.ExportEvent) error) error {
	var ts int64
	var key string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := s.d.Store.ExportEventPage(ctx, store.ExportEventParams{CanonicalJID: x.jid, From: x.from, To: x.to, AfterTS: ts, AfterKey: key, Limit: exportPageSize})
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, e := range page {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = fn(e); err != nil {
				return err
			}
			ts, key = e.TS, e.Key
		}
		if len(page) < exportPageSize {
			return nil
		}
	}
}

type messageIter struct {
	s    *Server
	ctx  context.Context
	x    exportScope
	page []store.ExportRecord
	at   int
	ts   int64
	id   string
	done bool
}

func (i *messageIter) next() (*store.ExportRecord, error) {
	if i.done {
		return nil, nil
	}
	if i.at >= len(i.page) {
		p, err := i.s.d.Store.ExportPage(i.ctx, store.ExportParams{CanonicalJID: i.x.jid, From: i.x.from, To: i.x.to, AfterTS: i.ts, AfterID: i.id, Limit: exportPageSize})
		if err != nil {
			return nil, err
		}
		i.page, i.at = p, 0
		if len(p) == 0 {
			i.done = true
			return nil, nil
		}
	}
	r := &i.page[i.at]
	i.at++
	i.ts, i.id = r.Message.Timestamp, r.Message.ID
	return r, nil
}

type eventIter struct {
	s    *Server
	ctx  context.Context
	x    exportScope
	page []store.ExportEvent
	at   int
	ts   int64
	key  string
	done bool
}

func (i *eventIter) next() (*store.ExportEvent, error) {
	if i.done {
		return nil, nil
	}
	if i.at >= len(i.page) {
		p, err := i.s.d.Store.ExportEventPage(i.ctx, store.ExportEventParams{CanonicalJID: i.x.jid, From: i.x.from, To: i.x.to, AfterTS: i.ts, AfterKey: i.key, Limit: exportPageSize})
		if err != nil {
			return nil, err
		}
		i.page, i.at = p, 0
		if len(p) == 0 {
			i.done = true
			return nil, nil
		}
	}
	e := &i.page[i.at]
	i.at++
	i.ts, i.key = e.TS, e.Key
	return e, nil
}

// eachTimeline merges the two bounded database cursors without loading a full chat. Events attached to messages are
// emitted from the event cursor, so a late edit remains at its true time instead of next to the old message.
func (s *Server) eachTimeline(ctx context.Context, x exportScope, fn func(*store.ExportRecord, *store.ExportEvent) error) error {
	mi := messageIter{s: s, ctx: ctx, x: x}
	ei := eventIter{s: s, ctx: ctx, x: x}
	m, err := mi.next()
	if err != nil {
		return err
	}
	e, err := ei.next()
	if err != nil {
		return err
	}
	for m != nil || e != nil {
		if err = ctx.Err(); err != nil {
			return err
		}
		takeMessage := e == nil || (m != nil && (m.Message.Timestamp < e.TS || (m.Message.Timestamp == e.TS && "m:"+m.Message.ID <= e.Key)))
		if takeMessage {
			if err = fn(m, nil); err != nil {
				return err
			}
			m, err = mi.next()
		} else {
			if err = fn(nil, e); err != nil {
				return err
			}
			e, err = ei.next()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func messageDisplay(m store.Message) string {
	if m.Text != "" {
		return m.Text
	}
	if m.Caption != "" {
		return m.Caption
	}
	var d struct {
		Name, Address, Question, DisplayName, Type string
		Options                                    []string
		Contacts                                   []struct {
			DisplayName string `json:"display_name"`
		}
	}
	_ = json.Unmarshal([]byte(m.Details), &d)
	parts := []string{d.Question, d.Name, d.Address, d.DisplayName}
	parts = append(parts, d.Options...)
	for _, c := range d.Contacts {
		parts = append(parts, c.DisplayName)
	}
	if d.Type != "" {
		parts = append(parts, d.Type)
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 && m.Kind != "" {
		return "[" + m.Kind + "]"
	}
	return strings.Join(out, " · ")
}

func exportEventLabel(e store.ExportEvent) string {
	if e.Message != nil {
		return eventLabel(*e.Message)
	}
	if e.Chat != nil {
		if e.Chat.Value != "" {
			return fmt.Sprintf("[chat %s] %s", e.Chat.Kind, e.Chat.Value)
		}
		return "[chat " + e.Chat.Kind + "]"
	}
	return ""
}
func exportEventSender(e store.ExportEvent) string {
	if e.Message != nil {
		return e.Message.SenderJID
	}
	if e.Chat != nil {
		return e.Chat.SenderJID
	}
	return ""
}
