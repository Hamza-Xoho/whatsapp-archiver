package main

import (
	"context"
	"log/slog"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

// newContactSync copies contact names from src (whatsmeow's contact store, a local read) into participants.contact_name
// through the store's single writer. The caller runs it among the service loops and feeds it the client's events with
// withContactSync.
func newContactSync(src wa.ContactNameSource, st *store.Store, log *slog.Logger) *wa.ContactSyncer {
	return wa.NewContactSyncer(src, func(ctx context.Context, names []wa.ContactName) (int, error) {
		batch := make([]store.ContactName, len(names))
		for i, n := range names {
			batch[i] = store.ContactName{JID: n.JID, Name: n.Name}
		}
		return st.SetContactNames(ctx, batch)
	}, log)
}

// withContactSync shows every event to s after h has handled it and returns h's verdict unchanged, so an event is
// acknowledged exactly when h stored it.
func withContactSync(s *wa.ContactSyncer, h func(any) bool) func(any) bool {
	return func(evt any) bool {
		ok := h(evt)
		s.Notify(evt)
		return ok
	}
}
