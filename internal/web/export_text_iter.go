package web

import (
	"context"

	"whatsapp-archiver/internal/store"
)

type textMessageIter struct {
	s       *Server
	ctx     context.Context
	x       exportScope
	stream  *store.ExportTextStream
	page    []store.ExportTextRecord
	at      int
	started bool
	done    bool
}

func (i *textMessageIter) next() (*store.ExportTextRecord, error) {
	if i.done {
		return nil, nil
	}
	if !i.started {
		stream, err := i.s.d.Store.OpenExportPageStream(i.ctx, store.ExportParams{CanonicalJID: i.x.jid, Snapshot: i.x.snapshot,
			From: i.x.from, To: i.x.to, SkipEvents: true, SkipMentions: true, Limit: exportPageSize})
		if err != nil {
			return nil, err
		}
		i.stream, i.started = stream, true
	}
	if i.at >= len(i.page) {
		page, err := i.stream.NextPage(i.ctx)
		if err != nil {
			return nil, err
		}
		i.page, i.at = page, 0
		if len(page) == 0 {
			i.done = true
			return nil, nil
		}
	}
	r := i.page[i.at]
	i.at++
	return &r, nil
}

func (i *textMessageIter) close() {
	if i.stream != nil {
		_ = i.stream.Close()
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
