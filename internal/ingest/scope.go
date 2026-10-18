package ingest

import "fmt"

type Filter struct {
	exclude map[string]struct{}
	aliases func(string) ([]string, error)
}

// NewFilter builds the scope filter. aliases may be nil; when it fails, Keep reports the error instead of guessing.
func NewFilter(exclude []string, aliases func(string) ([]string, error)) *Filter {
	set := make(map[string]struct{}, len(exclude))
	for _, jid := range exclude {
		set[jid] = struct{}{}
	}
	return &Filter{exclude: set, aliases: aliases}
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

// Excluded reports whether the chat is in the exclusion list, directly or through one of its aliases. It is the
// exclusion part of Keep, shared with the recorder; a failed alias lookup is an error, never an answer.
func (f *Filter) Excluded(chatJID string) (bool, error) {
	if _, ok := f.exclude[chatJID]; ok {
		return true, nil
	}
	if f.aliases != nil {
		aliases, err := f.aliases(chatJID)
		if err != nil {
			return false, fmt.Errorf("alias lookup for %s: %w", chatJID, err)
		}
		for _, alias := range aliases {
			if _, ok := f.exclude[alias]; ok {
				return true, nil
			}
		}
	}
	return false, nil
}
