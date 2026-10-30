package wa

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// ContactNameSource is the read of whatsmeow's contact store, which lives in session.db and is read through
// whatsmeow's own API: session.db is never copied and nothing is sent. *Client.ContactNames returns the real one.
type ContactNameSource interface {
	GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error)
}

// ContactName is one name for one JID, the JID as whatsmeow gives it (CON-7).
type ContactName struct{ JID, Name string }

// ContactNames picks the contact name of every contact that has one, in JID order: the address-book full name, else
// the first name, else the verified business name. The push name is a different column (participants.push_name) and
// never used here. Blanks count as empty.
func ContactNames(all map[types.JID]types.ContactInfo) []ContactName {
	out := make([]ContactName, 0, len(all))
	for j, info := range all {
		for _, name := range []string{info.FullName, info.FirstName, info.BusinessName} {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, ContactName{JID: j.String(), Name: name})
				break
			}
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].JID < out[k].JID })
	return out
}

// ContactNames returns a proxy so a syncer created before re-pairing reads the replacement device's contact store.
func (c *Client) ContactNames() ContactNameSource { return c }

func (c *Client) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	c.clientMu.RLock()
	defer c.clientMu.RUnlock()
	return c.client.Store.Contacts.GetAllContacts(ctx)
}

// contactDebounce is how long a sync waits after the first event that asks for one, so a burst (an app-state sync
// delivers hundreds of contact changes) costs one read and one write.
const contactDebounce = 5 * time.Second

// ContactSyncer copies contact names from the source to the sink: after the client connects, when the app-state
// patch that carries contacts has synced, and when a contact or business name changes. Events only queue a request
// (Notify never blocks); Run does the reading and writing, one sync at a time.
type ContactSyncer struct {
	src      ContactNameSource
	sink     func(context.Context, []ContactName) (int, error)
	log      *slog.Logger
	kick     chan struct{} // at most one request is waiting
	debounce time.Duration
	wait     func(context.Context, time.Duration) error
}

// NewContactSyncer returns a syncer that reads src and hands the names to sink, which reports how many changed.
func NewContactSyncer(src ContactNameSource, sink func(context.Context, []ContactName) (int, error), log *slog.Logger) *ContactSyncer {
	if log == nil {
		log = slog.Default()
	}
	return &ContactSyncer{src: src, sink: sink, log: log, kick: make(chan struct{}, 1), debounce: contactDebounce, wait: sleepCtx}
}

// WithDebounce replaces the debounce (tests use 0).
func (s *ContactSyncer) WithDebounce(d time.Duration) *ContactSyncer {
	s.debounce = d
	return s
}

// asksForSync is true for the events after which the contact store may hold new names. A push name is not one: it
// is not a contact name.
func asksForSync(evt any) bool {
	switch e := evt.(type) {
	case *events.Connected, *events.Contact, *events.BusinessName:
		return true
	case *events.AppStateSyncComplete:
		return e.Name == appstate.WAPatchCriticalUnblockLow
	}
	return false
}

// Notify queues a sync when evt asks for one. It never blocks and is safe to call from the event handler.
func (s *ContactSyncer) Notify(evt any) {
	if !asksForSync(evt) {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default: // one is already waiting
	}
}

// Run syncs until ctx ends. A request starts a debounce wait; requests that arrive during it join the same sync, one
// that arrives while the sync runs makes the next. Errors are logged and the loop goes on.
func (s *ContactSyncer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		}
		if err := s.wait(ctx, s.debounce); err != nil {
			return
		}
		select { // requests during the wait are served by this sync
		case <-s.kick:
		default:
		}
		s.syncOnce(ctx)
	}
}

func (s *ContactSyncer) syncOnce(ctx context.Context) {
	all, err := s.src.GetAllContacts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("contact names not read; they are tried again at the next contact event", "err", err)
		}
		return
	}
	names := ContactNames(all)
	if len(names) == 0 {
		return
	}
	changed, err := s.sink(ctx, names)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("contact names not written; they are tried again at the next contact event", "err", err)
		}
		return
	}
	s.log.Info("contact names synced", "contacts", len(names), "changed", changed)
}
