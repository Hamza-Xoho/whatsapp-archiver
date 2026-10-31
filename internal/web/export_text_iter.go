package web

import (
	"context"

	"whatsapp-archiver/internal/store"
)

type textMessageIter struct {
	s       *Server
	ctx     context.Context
	x       exportScope
	rows    *store.ExportTextRows
	names   map[string]string
	started bool
}

func (i *textMessageIter) next() (*store.ExportTextRecord, error) {
	if !i.started {
		rows, err := i.s.d.Store.OpenExportText(i.ctx, i.x.jid, i.x.from, i.x.to)
		if err != nil {
			return nil, err
		}
		i.rows, i.names, i.started = rows, make(map[string]string), true
	}
	if !i.rows.Next() {
		return nil, i.rows.Err()
	}
	record, err := i.rows.Scan()
	if err != nil {
		return nil, err
	}
	sender := record.Message.SenderJID
	if sender != "" {
		name, ok := i.names[sender]
		if !ok {
			name, err = i.s.d.Store.ChatName(i.ctx, sender)
			if err != nil {
				return nil, err
			}
			i.names[sender] = name
		}
		if name != "" {
			record.Message.PushName = name
		}
	}
	return &record, nil
}

func (i *textMessageIter) close() {
	if i.rows != nil {
		_ = i.rows.Close()
	}
}

// eachTextTimeline merges a constant-memory message cursor with the complete event cursor.
func (s *Server) eachTextTimeline(ctx context.Context, x exportScope, fn func(*store.ExportTextRecord, *store.ExportEvent) error) error {
	messages := textMessageIter{s: s, ctx: ctx, x: x}
	defer messages.close()
	events := eventIter{s: s, ctx: ctx, x: x}
	message, err := messages.next()
	if err != nil {
		return err
	}
	event, err := events.next()
	if err != nil {
		return err
	}
	for message != nil || event != nil {
		if err = ctx.Err(); err != nil {
			return err
		}
		takeMessage := event == nil || (message != nil && (message.Message.Timestamp < event.TS ||
			(message.Message.Timestamp == event.TS && "m:"+message.Message.ID <= event.Key)))
		if takeMessage {
			if err = fn(message, nil); err == nil {
				message, err = messages.next()
			}
		} else {
			if err = fn(nil, event); err == nil {
				event, err = events.next()
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
