package wa

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// ContactNameSource is the read of the contact store (red stub).
type ContactNameSource interface {
	GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error)
}

// ContactName is one name for one JID.
type ContactName struct{ JID, Name string }

// ContactNames picks the names (red stub).
func ContactNames(map[types.JID]types.ContactInfo) []ContactName { return nil }

// ContactSyncer (red stub).
type ContactSyncer struct {
	src      ContactNameSource
	sink     func(context.Context, []ContactName) (int, error)
	log      *slog.Logger
	kick     chan struct{}
	debounce time.Duration
	wait     func(context.Context, time.Duration) error
}

// NewContactSyncer (red stub).
func NewContactSyncer(src ContactNameSource, sink func(context.Context, []ContactName) (int, error), log *slog.Logger) *ContactSyncer {
	return &ContactSyncer{src: src, sink: sink, log: log, kick: make(chan struct{}, 1), debounce: contactDebounce, wait: sleepCtx}
}

const contactDebounce = time.Second

// WithDebounce sets the debounce (red stub).
func (s *ContactSyncer) WithDebounce(d time.Duration) *ContactSyncer { return s }

// Notify (red stub).
func (s *ContactSyncer) Notify(any) {}

// Run (red stub).
func (s *ContactSyncer) Run(context.Context) {}

// ContactNames returns the client's contact store as the source (red stub).
func (c *Client) ContactNames() ContactNameSource { return nil }
