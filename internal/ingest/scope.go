package ingest

import "fmt"

type Filter struct {
	rules   *RuleSet
	aliases func(string) ([]string, error)
}

// NewFilter builds the scope filter over the live rule set (nil: no exclusions). aliases may be nil; when it fails,
// Keep reports the error instead of guessing.
func NewFilter(rules *RuleSet, aliases func(string) ([]string, error)) *Filter {
	return &Filter{rules: rules, aliases: aliases}
}

// Keep reports whether an event of this chat and kind is archived. A failed alias lookup is an error and never an
// answer: guessing "keep" would store an excluded chat seen under its other alias, so callers retry until it succeeds.
func (f *Filter) Keep(chatJID string, kind Kind) (bool, error) {
	if chatJID == "status@broadcast" {
		return false, nil
	}
	switch kind {
	case KindPresence, KindTyping, KindReceipt, KindCall:
		return false, nil
	}
	excluded, err := f.Excluded(chatJID)
	if err != nil {
		return false, err
	}
	return !excluded, nil
}

// Excluded reports whether logging is off for the chat, directly or through one of its aliases. It is the
// exclusion part of Keep, shared with the recorder; a failed alias lookup is an error, never an answer.
func (f *Filter) Excluded(chatJID string) (bool, error) {
	if f.rules.Excluded(chatJID) {
		return true, nil
	}
	if f.aliases != nil {
		aliases, err := f.aliases(chatJID)
		if err != nil {
			return false, fmt.Errorf("alias lookup for %s: %w", chatJID, err)
		}
		for _, alias := range aliases {
			if f.rules.Excluded(alias) {
				return true, nil
			}
		}
	}
	return false, nil
}
