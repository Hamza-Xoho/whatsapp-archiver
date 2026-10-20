package web

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strconv"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// maxFormBytes bounds a chats form; ten thousand selected chats fit well inside it.
const maxFormBytes = 1 << 20

// mediaTypes are the media types a chat's own rules can choose to download.
var mediaTypes = []string{"image", "video", "audio", "document", "sticker", "gif"}

type chatRowView struct {
	store.ChatRow
	RowID, LastMessage string
	OOB                bool // the row replaces its copy on the page (an htmx out-of-band swap)
}

type chatsData struct {
	page
	Query      string
	Rows       []chatRowView
	MediaTypes []string
}

// chats is GET /chats?q=, the chat list. Like every GET it only reads.
func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	rows, err := s.chatRows(r.Context(), q, nil, false)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["chats"], "layout", chatsData{page: page{Title: "Chats"}, Query: q, Rows: rows, MediaTypes: mediaTypes})
}

// chatRows is the list filtered by q; with only set, just those chats. oob marks the rows for an out-of-band swap.
func (s *Server) chatRows(ctx context.Context, q string, only map[string]bool, oob bool) ([]chatRowView, error) {
	if s.d.Store == nil {
		return nil, nil
	}
	list, err := s.d.Store.ChatList(ctx, q)
	if err != nil {
		return nil, err
	}
	loc, err := view.Location(s.config())
	if err != nil {
		return nil, err
	}
	out := make([]chatRowView, 0, len(list))
	for _, c := range list {
		if only != nil && !only[c.CanonicalJID] {
			continue
		}
		out = append(out, chatRowView{ChatRow: c, RowID: rowID(c.CanonicalJID), LastMessage: view.FormatUnix(loc, c.Newest), OOB: oob})
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
	change, err := parseRuleForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.d.Store == nil {
		http.Error(w, "no archive", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	existing, err := s.d.Store.ChatRules(ctx)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	byJID := make(map[string]store.ChatRule, len(existing))
	for _, rule := range existing {
		byJID[rule.JID] = rule
	}
	now := s.d.Now().Unix()
	var write []store.ChatRule
	written := map[string]bool{}
	for _, jid := range change.jids {
		aliases, err := s.d.Store.Aliases(ctx, jid)
		if err != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
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
			rule.UpdatedAt = now
			write = append(write, rule)
		}
	}
	if err = s.d.Store.SetChatRules(ctx, write); err != nil {
		http.Error(w, "the change could not be saved", http.StatusInternalServerError)
		return
	}
	all, err := s.d.Store.ChatRules(ctx)
	if err != nil {
		http.Error(w, "the change was saved but could not be applied; restart the archiver", http.StatusInternalServerError)
		return
	}
	if s.d.Rules != nil {
		s.d.Rules.Replace(all)
	}
	s.updatedRows(w, r, change.jids)
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
	rows, err := s.chatRows(r.Context(), "", only, true)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["chats"], "chat_rows", rows)
}
