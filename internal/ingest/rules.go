package ingest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

// RuleSet is the live set of chat rules the capture path reads. Replace swaps it whole, so the next event sees the
// new rules without a restart. A nil RuleSet has no rules: every chat is logged with the global media rules.
type RuleSet struct{ cur atomic.Pointer[ruleState] }

type ruleState struct {
	excluded map[string]struct{}
	media    map[string]*config.MediaRules
}

func NewRuleSet(rules []store.ChatRule) *RuleSet {
	r := &RuleSet{}
	r.Replace(rules)
	return r
}

// Replace installs rules as the full rule set.
func (r *RuleSet) Replace(rules []store.ChatRule) {
	st := &ruleState{excluded: map[string]struct{}{}, media: map[string]*config.MediaRules{}}
	for _, rule := range rules {
		if !rule.Logging {
			st.excluded[rule.JID] = struct{}{}
		}
		if rule.Media != nil {
			m := *rule.Media
			st.media[rule.JID] = &m
		}
	}
	r.cur.Store(st)
}

// Excluded reports whether logging is off for the chat with exactly this JID. Aliases are the Filter's job.
func (r *RuleSet) Excluded(jid string) bool {
	if r == nil {
		return false
	}
	_, ok := r.cur.Load().excluded[jid]
	return ok
}

// Media returns the chat's own media rules for exactly this JID, or nil when the global rules apply.
func (r *RuleSet) Media(jid string) *config.MediaRules {
	if r == nil {
		return nil
	}
	return r.cur.Load().media[jid]
}

// ExclusionRules turns a list of excluded chats (config.yaml's exclude, replay) into rules with logging off.
func ExclusionRules(jids []string) []store.ChatRule {
	out := make([]store.ChatRule, 0, len(jids))
	for _, jid := range jids {
		out = append(out, store.ChatRule{JID: jid, Logging: false})
	}
	return out
}

type SeenStore interface {
	MarkChatSeen(ctx context.Context, jid string, at int64) (bool, error)
	SeenChats(ctx context.Context) ([]store.ChatSeen, error)
}

// SeenTracker records the first sighting of each chat. It keeps the chats already seen in memory (loaded at start),
// so chat_seen is written once per new chat and never once per message.
type SeenTracker struct {
	store SeenStore
	mu    sync.Mutex
	seen  map[string]struct{}
}

func NewSeenTracker(ctx context.Context, s SeenStore) (*SeenTracker, error) {
	known, err := s.SeenChats(ctx)
	if err != nil {
		return nil, fmt.Errorf("load seen chats: %w", err)
	}
	t := &SeenTracker{store: s, seen: make(map[string]struct{}, len(known))}
	for _, c := range known {
		t.seen[c.JID] = struct{}{}
	}
	return t, nil
}

// mark records jid's first sighting at at; a chat already seen costs no write. A failed write leaves the chat unseen,
// so the next attempt writes it again.
func (t *SeenTracker) mark(ctx context.Context, jid string, at int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.seen[jid]; ok {
		return nil
	}
	if _, err := t.store.MarkChatSeen(ctx, jid, at); err != nil {
		return err
	}
	t.seen[jid] = struct{}{}
	return nil
}

// WithSeen makes Ingest record the first sighting of each kept chat in t. Without it no sighting is recorded.
func (p *Pipeline) WithSeen(t *SeenTracker) *Pipeline {
	p.seen = t
	return p
}
