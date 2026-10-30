package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
)

const chatsPageSize = 50

type chatFilters struct {
	Query, Type, Logging string
	New, Gaps, Failed    bool
}

func parseChatFilters(v url.Values) chatFilters {
	return chatFilters{Query: strings.TrimSpace(v.Get("q")), Type: v.Get("type"), Logging: v.Get("logging"),
		New: v.Get("new") == "1", Gaps: v.Get("gaps") == "1", Failed: v.Get("failed") == "1"}
}

func parsePostedChatFilters(v url.Values) chatFilters {
	f := parseChatFilters(v)
	f.Logging = v.Get("logging_filter")
	return f
}

func (f chatFilters) active() bool {
	return f.Query != "" || f.Type != "" || f.Logging != "" || f.New || f.Gaps || f.Failed
}

func (f chatFilters) match(c store.ChatRow, typ string) bool {
	if f.Type != "" && f.Type != typ {
		return false
	}
	if f.Logging == "on" && !c.Logging || f.Logging == "off" && c.Logging {
		return false
	}
	return (!f.New || c.New) && (!f.Gaps || c.OpenGaps > 0) && (!f.Failed || c.MediaFailed > 0)
}

func (f chatFilters) values() url.Values {
	v := url.Values{}
	for key, value := range map[string]string{"q": f.Query, "type": f.Type, "logging": f.Logging} {
		if value != "" {
			v.Set(key, value)
		}
	}
	for key, yes := range map[string]bool{"new": f.New, "gaps": f.Gaps, "failed": f.Failed} {
		if yes {
			v.Set(key, "1")
		}
	}
	return v
}

func normalizedChatType(typ, jid string) string {
	switch typ {
	case "community":
		return "community"
	case "group":
		return "group"
	case "direct", "individual":
		return "direct"
	}
	if strings.HasSuffix(jid, "@g.us") {
		return "group"
	}
	return "direct"
}

type pageCursor struct {
	After, Previous string
}

func encodeCursor(c pageCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (pageCursor, error) {
	if raw == "" {
		return pageCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageCursor{}, err
	}
	var c pageCursor
	if err = json.Unmarshal(b, &c); err != nil || c.After == "" {
		return pageCursor{}, fmt.Errorf("bad cursor")
	}
	return c, nil
}

func paginateChats(rows []chatRowView, raw string, filters chatFilters) ([]chatRowView, string, string, error) {
	cursor, err := decodeCursor(raw)
	if err != nil {
		return nil, "", "", err
	}
	start := 0
	if cursor.After != "" {
		start = -1
		for i := range rows {
			if rows[i].CanonicalJID == cursor.After {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, "", "", fmt.Errorf("cursor chat is no longer in the result")
		}
	}
	end := min(start+chatsPageSize, len(rows))
	page := rows[start:end]
	base := filters.values()
	var next, prev string
	if end < len(rows) {
		base.Set("cursor", encodeCursor(pageCursor{After: rows[end-1].CanonicalJID, Previous: cursor.After}))
		next = "/chats?" + base.Encode()
	}
	if start > 0 {
		base = filters.values()
		if cursor.Previous != "" {
			var previousPrevious string
			for i := range rows {
				if rows[i].CanonicalJID == cursor.Previous && i >= chatsPageSize {
					previousPrevious = rows[i-chatsPageSize].CanonicalJID
				}
			}
			base.Set("cursor", encodeCursor(pageCursor{After: cursor.Previous, Previous: previousPrevious}))
		}
		prev = "/chats"
		if encoded := base.Encode(); encoded != "" {
			prev += "?" + encoded
		}
	}
	return page, next, prev, nil
}

func chatSnapshot(rows []chatRowView) string {
	h := sha256.New()
	for _, row := range rows {
		_, _ = h.Write([]byte(row.CanonicalJID + "\n"))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (s *Server) filteredFormJIDs(r *http.Request) ([]string, error) {
	filters := parsePostedChatFilters(r.PostForm)
	rows, err := s.chatRows(r.Context(), filters, nil, false)
	if err != nil {
		return nil, fmt.Errorf("the archive could not be read")
	}
	if r.PostForm.Get("snapshot") == "" || r.PostForm.Get("snapshot") != chatSnapshot(rows) {
		return nil, fmt.Errorf("the filtered set changed; review the new count and estimate before applying")
	}
	jids := make([]string, len(rows))
	for i := range rows {
		jids[i] = rows[i].CanonicalJID
	}
	return jids, nil
}

func sameRule(a, b *store.ChatRule) bool {
	if a == nil || b == nil || a.Logging != b.Logging {
		return false
	}
	ab, _ := json.Marshal(a.Media)
	bb, _ := json.Marshal(b.Media)
	return string(ab) == string(bb)
}

func expandCommunityTargets(jids []string, controls map[string]store.ChatControl) []string {
	out := slices.Clone(jids)
	for _, jid := range jids {
		community := controls[jid]
		if community.Type != "community" {
			continue
		}
		for child, control := range controls {
			if control.Parent == jid && (control.Rule == nil || sameRule(control.Rule, community.Rule)) && !slices.Contains(out, child) {
				out = append(out, child)
			}
		}
	}
	return out
}

func (s *Server) effectiveRules(ctx context.Context) ([]store.ChatRule, error) {
	rules, err := s.d.Store.ChatRules(ctx)
	if err != nil {
		return nil, err
	}
	controls, err := s.d.Store.ChatControls(ctx)
	if err != nil {
		return nil, err
	}
	for jid, control := range controls {
		if control.Parent == "" || control.Rule != nil {
			continue
		}
		if parent := controls[control.Parent].Rule; parent != nil {
			copy := *parent
			copy.JID = jid
			rules = append(rules, copy)
		}
	}
	return rules, nil
}

type rulePreviewData struct {
	Rows                      []chatRowView
	Count                     int
	TextPerWeek, MediaPerWeek string
	Basis                     string
	Unavailable               bool
	Scope, Snapshot           string
	Filters                   chatFilters
	Logging, Media, VideoMax  string
	Downloads                 []string
}

func (s *Server) chatRulePreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	if all, syncErr := s.effectiveRules(r.Context()); syncErr == nil && s.d.Rules != nil {
		s.d.Rules.Replace(all)
	}
	if r.PostForm.Get("scope") == "filtered" {
		jids, err := s.filteredFormJIDs(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		r.PostForm["jid[]"] = jids
	}
	change, err := parseRuleForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.chatRows(r.Context(), chatFilters{}, jidSet(change.jids), false)
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	estimate, err := s.d.Store.EstimateChats(r.Context(), change.jids)
	if err != nil {
		http.Error(w, "the estimate could not be prepared", http.StatusInternalServerError)
		return
	}
	data := rulePreviewData{Rows: rows, Count: len(change.jids), Scope: r.PostForm.Get("scope"),
		Snapshot: r.PostForm.Get("snapshot"), Filters: parsePostedChatFilters(r.PostForm), Logging: r.PostForm.Get("logging"),
		Media: r.PostForm.Get("media"), VideoMax: r.PostForm.Get("video_max_mb"), Downloads: r.PostForm["download"]}
	data.Unavailable = estimate.Messages < 2 || estimate.Newest <= estimate.Oldest
	if data.Unavailable {
		data.Basis = "Estimate based on available archive history: unavailable because there is not enough history yet."
	} else {
		window := time.Duration(estimate.Newest-estimate.Oldest) * time.Second
		weeks := float64((7 * 24 * time.Hour)) / float64(window)
		data.TextPerWeek = formatBytes(int64(float64(estimate.TextBytes) * weeks))
		data.MediaPerWeek = formatBytes(int64(float64(estimate.MediaBytes) * weeks))
		data.Basis = fmt.Sprintf("Estimate based on %d archived messages over %s.", estimate.Messages, window.Round(time.Hour))
	}
	s.render(w, s.pages["chats"], "rule_preview", data)
}

func jidSet(jids []string) map[string]bool {
	out := make(map[string]bool, len(jids))
	for _, jid := range jids {
		out[jid] = true
	}
	return out
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (s *Server) chatRuleInherit(w http.ResponseWriter, r *http.Request) {
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
	s.rulesMu.Lock()
	err := s.d.Store.DeleteChatRules(r.Context(), jids)
	if err == nil {
		var all []store.ChatRule
		all, err = s.effectiveRules(r.Context())
		if err == nil && s.d.Rules != nil {
			s.d.Rules.Replace(all)
		}
	}
	s.rulesMu.Unlock()
	if err != nil {
		http.Error(w, "the change could not be saved", http.StatusInternalServerError)
		return
	}
	s.updatedRows(w, r, jids)
}
