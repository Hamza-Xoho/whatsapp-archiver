package main

import (
	"log/slog"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/wa"
)

// newContactSync is the red stub.
func newContactSync(src wa.ContactNameSource, st *store.Store, log *slog.Logger) *wa.ContactSyncer {
	return wa.NewContactSyncer(src, nil, log)
}

// withContactSync is the red stub.
func withContactSync(s *wa.ContactSyncer, h func(any) bool) func(any) bool { return h }
