package web

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strconv"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// maxFormBytes bounds a chats form; ten thousand selected chats fit well inside it.
const maxFormBytes = 1 << 20

// mediaTypes are the media types a chat's own rules can choose to download.
var mediaTypes = config.MediaTypes

type chatRowView struct {
	store.ChatRow
	RowID, LastMessage, HistoryBackTo   string
	Type, Parent, ParentName, RuleLabel string
	ChildCount                          int
	Children                            []chatChildView
	MediaTypes                          []string
	StopConfirm                         string
	OOB                                 bool // the row replaces its copy on the page (an htmx out-of-band swap)
}

type chatChildView struct{ JID, Name string }

type chatsData struct {
	page
	Filters     chatFilters
	Rows        []chatRowView
	MediaTypes  []string
	Total       int
	Snapshot    string
	Next, Prev  string
	HasFilters  bool
	FilterChips []filterChip
}

type filterChip struct{ Key, Label, Href string }

// chats is GET /chats, the filtered, keyset-paged chat list. Like every GET it only reads.
func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	filters := parseChatFilters(r.URL.Query())
	rawCursor := r.URL.Query().Get("cursor")
	cursor, err := decodeCursor(rawCursor)
	if err != nil {
		http.Error(w, "invalid page cursor", http.StatusBadRequest)
		return
	}
	var list []store.ChatRow
	var membership []string
	var total int
	var more bool
	if s.d.Store != nil {
		view, readErr := s.d.Store.ChatListView(r.Context(), filters.storeFilter(), store.ChatListAfter{
			JID: cursor.After, Last: cursor.Last, Messages: cursor.Messages,
		}, chatsPageSize)
		if readErr != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
		}
		list, membership, total, more = view.Rows, view.JIDs, view.Total, view.More
	}
	rows, err := s.chatRowViews(r.Context(), list, nil, false)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	if s.d.Store != nil {
		err = s.refreshEffectiveRules(r.Context())
	}
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	next, prev := pageLinks(filters, rawCursor, cursor, rows, more)
	s.render(w, s.pages["chats"], "layout", chatsData{page: page{Title: "Chats", Current: "Chats"}, Filters: filters,
		Rows: rows, MediaTypes: mediaTypes, Total: total, Snapshot: chatFilterSnapshot(filters, membership), Next: next, Prev: prev,
		HasFilters: filters.active(), FilterChips: filterChips(filters)})
}

// chatRows reads the derived chat list and small control tables. With only set, it returns just those rows.
func (s *Server) chatRows(ctx context.Context, filters chatFilters, only map[string]bool, oob bool) ([]chatRowView, error) {
	if s.d.Store == nil {
		return nil, nil
	}
	list, _, _, err := s.d.Store.ChatListPage(ctx, filters.storeFilter(), store.ChatListAfter{}, 10000)
	if err != nil {
		return nil, err
	}
	return s.chatRowViews(ctx, list, only, oob)
}

func (s *Server) chatRowViews(ctx context.Context, list []store.ChatRow, only map[string]bool, oob bool) ([]chatRowView, error) {
	if s.d.Store == nil {
		return nil, nil
	}
	controls, err := s.d.Store.ChatControls(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := view.Location(s.config())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(list))
	for _, c := range list {
		names[c.CanonicalJID] = c.DisplayName
	}
	children := map[string][]chatChildView{}
	for child, c := range controls {
		if c.Parent != "" {
			children[c.Parent] = append(children[c.Parent], chatChildView{JID: child, Name: names[child]})
		}
	}
	out := make([]chatRowView, 0, len(list))
	for _, c := range list {
		if only != nil && !only[c.CanonicalJID] {
			continue
		}
		control := controls[c.CanonicalJID]
		typ := normalizedChatType(control.Type, c.CanonicalJID)
		label := "Default"
		if control.Parent != "" {
			parent := controls[control.Parent]
			if (control.Rule == nil || control.Rule.InheritedFrom != "") && parent.Rule != nil {
				c.Logging, c.HasMediaRule = parent.Rule.Logging, parent.Rule.Media != nil
			}
			if parent.Rule != nil && (control.Rule == nil || control.Rule.InheritedFrom != "") {
				label = "Inherited from " + names[control.Parent]
			} else if control.Rule != nil {
				label = "Own rule"
			}
		} else if control.Rule != nil {
			label = "Own rule"
		}
		kids := children[c.CanonicalJID]
		confirm := "Stop logging this chat? Messages that arrive while logging is off are not saved."
		if len(kids) > 0 {
			confirm = fmt.Sprintf("Stop logging %s? This also affects %d child chats.", c.DisplayName, len(kids))
		}
		row := chatRowView{ChatRow: c, RowID: rowID(c.CanonicalJID), LastMessage: view.FormatUnix(loc, c.Newest), OOB: oob,
			Type: typ, Parent: control.Parent, ParentName: names[control.Parent], RuleLabel: label, ChildCount: len(kids), Children: kids,
			MediaTypes: mediaTypes, StopConfirm: confirm}
		if c.OldestHistory > 0 {
			row.HistoryBackTo = time.Unix(c.OldestHistory, 0).In(loc).Format("2 Jan 2006")
		}
		out = append(out, row)
	}
	return out, nil
}

// rowID is a stable element id for a chat's row; the JID itself may hold characters an id selector cannot.
func rowID(jid string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(jid))
	return fmt.Sprintf("chat-%016x", h.Sum64())
}

// ruleChange is a parsed POST /chats/rules form. A nil field leaves that part of each chat's rule as it is.
type ruleChange struct {
	jids    []string
	logging *bool
	media   **config.MediaRules // set to a nil *MediaRules: back to the global rules
}

func parseRuleForm(r *http.Request) (ruleChange, error) {
	var c ruleChange
	c.jids = formJIDs(r)
	if len(c.jids) == 0 {
		return c, fmt.Errorf("no chat selected")
	}
	switch r.PostForm.Get("logging") {
	case "":
	case "on":
		c.logging = new(bool)
		*c.logging = true
	case "off":
		c.logging = new(bool)
	default:
		return c, fmt.Errorf("logging must be on or off")
	}
	var media *config.MediaRules
	switch r.PostForm.Get("media") {
	case "":
	case "default":
		c.media = &media
	case "text-only":
		media = &config.MediaRules{Download: []string{}}
		c.media = &media
	case "custom":
		media = &config.MediaRules{Download: []string{}}
		for _, t := range r.PostForm["download"] {
			if !slices.Contains(mediaTypes, t) {
				return c, fmt.Errorf("unknown media type %q", t)
			}
			if !slices.Contains(media.Download, t) {
				media.Download = append(media.Download, t)
			}
		}
		if v := r.PostForm.Get("video_max_mb"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 100000 {
				return c, fmt.Errorf("video size cap must be a whole number of MB")
			}
			media.VideoMaxMB = n
		}
		c.media = &media
	default:
		return c, fmt.Errorf("unknown media choice")
	}
	if c.logging == nil && c.media == nil {
		return c, fmt.Errorf("nothing to change")
	}
	return c, nil
}

// formJIDs is the form's distinct, non-empty jid[] values.
func formJIDs(r *http.Request) []string {
	var out []string
	for _, j := range r.PostForm["jid[]"] {
		if j != "" && len(j) <= 256 && !slices.Contains(out, j) {
			out = append(out, j)
		}
	}
	return out
}

// chatRules is POST /chats/rules: it writes the change for every selected chat in one transaction, then replaces the
// live rule set with every rule in the archive, so capture applies it from the next event. A chat's rules stored
// under its other JID forms change with it, so no older rule on an alias overrides the new one.
func (s *Server) chatRules(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("scope") == "filtered" {
		jids, err := s.filteredFormJIDs(r)
		if err != nil {
			if stale, ok := err.(*staleFilterError); ok {
				s.renderStalePreview(w, r, stale)
			} else {
				http.Error(w, err.Error(), http.StatusConflict)
			}
			return
		}
		r.PostForm["jid[]"] = jids
	}
	change, err := parseRuleForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.d.Store == nil {
		http.Error(w, "no archive", http.StatusServiceUnavailable)
		return
	}
	if msg := s.applyRules(r.Context(), change); msg != "" {
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	s.updatedRows(w, r, change.jids)
}

// applyRules is the locked part of a rule change: read, merge, write, re-read and Replace run under rulesMu, so two
// interleaved changes cannot leave the live rules behind the archive. It returns "" or what went wrong.
func (s *Server) applyRules(ctx context.Context, change ruleChange) string {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	existing, err := s.d.Store.ExplicitChatRules(ctx)
	if err != nil {
		return "the archive could not be read"
	}
	byJID := make(map[string]store.ChatRule, len(existing))
	for _, rule := range existing {
		byJID[rule.JID] = rule
	}
	controls, err := s.d.Store.ChatControls(ctx)
	if err != nil {
		return "the archive could not be read"
	}
	selected := jidSet(change.jids)
	change.jids = expandCommunityTargets(change.jids, controls)
	now := s.d.Now().Unix()
	var write []store.ChatRule
	written := map[string]bool{}
	for _, jid := range change.jids {
		aliases, err := s.d.Store.Aliases(ctx, jid)
		if err != nil {
			return "the archive could not be read"
		}
		targets := []string{jid}
		for _, a := range aliases {
			if _, ok := byJID[a]; ok {
				targets = append(targets, a)
			}
		}
		for _, t := range targets {
			if written[t] {
				continue
			}
			written[t] = true
			rule, ok := byJID[t]
			if !ok {
				rule = store.ChatRule{JID: t, Logging: true}
			}
			if change.logging != nil {
				rule.Logging = *change.logging
			}
			if change.media != nil {
				rule.Media = *change.media
			}
			if selected[jid] {
				rule.InheritedFrom = ""
			} else {
				rule.InheritedFrom = controls[jid].Parent
			}
			rule.UpdatedAt = now
			write = append(write, rule)
		}
	}
	if err := s.d.Store.SetChatRules(ctx, write); err != nil {
		return "the change could not be saved"
	}
	all, err := s.effectiveRules(ctx)
	if err != nil {
		return "the change was saved but could not be applied; restart the archiver"
	}
	if s.d.Rules != nil {
		s.d.Rules.Replace(all)
	}
	return ""
}

// chatAck is POST /chats/ack: the selected chats stop being flagged New, under every JID form they were seen as.
func (s *Server) chatAck(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	jids := formJIDs(r)
	if len(jids) == 0 {
		http.Error(w, "no chat selected", http.StatusBadRequest)
		return
	}
	if s.d.Store == nil {
		http.Error(w, "no archive", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	all := slices.Clone(jids)
	for _, jid := range jids {
		aliases, err := s.d.Store.Aliases(ctx, jid)
		if err != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
		}
		all = append(all, aliases...)
	}
	if err := s.d.Store.AcknowledgeChats(ctx, all); err != nil {
		http.Error(w, "the change could not be saved", http.StatusInternalServerError)
		return
	}
	s.updatedRows(w, r, jids)
}

// updatedRows answers a chats POST: the changed rows for htmx to swap in place, or, for a plain form post (no
// JavaScript), a redirect back to the list.
func (s *Server) updatedRows(w http.ResponseWriter, r *http.Request, jids []string) {
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/chats", http.StatusSeeOther)
		return
	}
	only := make(map[string]bool, len(jids))
	for _, j := range jids {
		only[j] = true
	}
	rows, err := s.chatRows(r.Context(), chatFilters{}, only, true)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["chats"], "chat_rows", rows)
}
