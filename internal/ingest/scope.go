package ingest

type Filter struct {
	exclude map[string]struct{}
	aliases func(string) []string
}

func NewFilter(exclude []string, aliases func(string) []string) *Filter {
	set := make(map[string]struct{}, len(exclude))
	for _, jid := range exclude {
		set[jid] = struct{}{}
	}
	return &Filter{exclude: set, aliases: aliases}
}

func (f *Filter) Keep(chatJID string, kind Kind) bool {
	if chatJID == "status@broadcast" {
		return false
	}
	switch kind {
	case KindPresence, KindTyping, KindReceipt, KindCall:
		return false
	}
	if _, ok := f.exclude[chatJID]; ok {
		return false
	}
	if f.aliases != nil {
		for _, alias := range f.aliases(chatJID) {
			if _, ok := f.exclude[alias]; ok {
				return false
			}
		}
	}
	return true
}
