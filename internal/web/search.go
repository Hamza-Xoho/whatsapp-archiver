package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

const searchPageSize = 50

type searchResult struct {
	store.SearchHit
	Time     string
	Segments []highlightSegment
}

type highlightSegment struct {
	Text  string
	Match bool
}

func searchWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r)
}

func expandSearchWord(text string, start, end int) (int, int) {
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if !searchWordRune(r) {
			break
		}
		start -= size
	}
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !searchWordRune(r) {
			break
		}
		end += size
	}
	return start, end
}

// highlightSearch returns plain strings. html/template escapes every segment; only the template supplies <mark>.
func highlightSearch(text, query string) []highlightSegment {
	needle := []rune(strings.TrimSpace(strings.ReplaceAll(store.Fold(query), `"`, "")))
	if len(needle) == 0 || text == "" {
		return []highlightSegment{{Text: text}}
	}
	type mappedRune struct {
		value      rune
		start, end int
	}
	var mapped []mappedRune
	for start, original := range text {
		end := start + len(string(original))
		for _, folded := range store.Fold(string(original)) {
			mapped = append(mapped, mappedRune{value: folded, start: start, end: end})
		}
	}
	var matches [][2]int
	for i := 0; i+len(needle) <= len(mapped); {
		match := true
		for j := range needle {
			if mapped[i+j].value != needle[j] {
				match = false
				break
			}
		}
		if !match {
			i++
			continue
		}
		start, end := expandSearchWord(text, mapped[i].start, mapped[i+len(needle)-1].end)
		matches = append(matches, [2]int{start, end})
		i += len(needle)
	}
	if len(matches) == 0 {
		return []highlightSegment{{Text: text}}
	}
	segments := make([]highlightSegment, 0, len(matches)*2+1)
	at := 0
	for _, m := range matches {
		if m[0] > at {
			segments = append(segments, highlightSegment{Text: text[at:m[0]]})
		}
		segments = append(segments, highlightSegment{Text: text[m[0]:m[1]], Match: true})
		at = m[1]
	}
	if at < len(text) {
		segments = append(segments, highlightSegment{Text: text[at:]})
	}
	return segments
}

type searchGroup struct {
	JID, Name string
	Results   []searchResult
}

type searchData struct {
	page
	Query, Chat, Sender, From, To, Media string
	MediaKinds                           []string
	Groups                               []searchGroup
	ResultCount, ChatCount               int
	Searched, HasMore                    bool
	NextURL                              string
}

func parseSearchTime(raw string, end bool, loc *time.Location) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n <= 0 {
			return 0, strconv.ErrSyntax
		}
		return n, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return 0, err
	}
	if end {
		t = t.AddDate(0, 0, 1).Add(-time.Second)
	}
	return t.Unix(), nil
}

// search is GET /search. The URL owns every filter and cursor; empty queries render instructions and never call FTS.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := searchData{page: page{Title: "Search messages", Current: "Chats"}, Query: q.Get("q"), Chat: q.Get("chat"), Sender: q.Get("sender"), From: q.Get("from"), To: q.Get("to"), Media: q.Get("media"), MediaKinds: mediaKinds}
	data.Searched = strings.TrimSpace(store.Fold(data.Query)) != ""
	if s.d.Store == nil || !data.Searched {
		s.render(w, s.pages["search"], "layout", data)
		return
	}
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	from, err := parseSearchTime(data.From, false, loc)
	if err != nil {
		http.Error(w, "from must be a date", http.StatusBadRequest)
		return
	}
	to, err := parseSearchTime(data.To, true, loc)
	if err != nil {
		http.Error(w, "to must be a date", http.StatusBadRequest)
		return
	}
	if from > 0 && to > 0 && from > to {
		http.Error(w, "from must not be after to", http.StatusBadRequest)
		return
	}
	limit := searchPageSize
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > searchPageSize {
			http.Error(w, "limit must be between 1 and 50", http.StatusBadRequest)
			return
		}
	}
	var before int64
	if raw := q.Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			http.Error(w, "before must be a message time", http.StatusBadRequest)
			return
		}
	}
	hits, err := s.d.Store.Search(r.Context(), store.SearchParams{Query: data.Query, ChatJID: data.Chat, SenderJID: data.Sender,
		From: from, To: to, MediaType: data.Media, BeforeTS: before, BeforeChat: q.Get("before_chat"), BeforeID: q.Get("before_id"), Limit: limit})
	if err != nil {
		http.Error(w, "search is unavailable right now", http.StatusInternalServerError)
		return
	}
	groupAt := map[string]int{}
	for _, h := range hits {
		i, ok := groupAt[h.CanonicalJID]
		if !ok {
			i = len(data.Groups)
			groupAt[h.CanonicalJID] = i
			data.Groups = append(data.Groups, searchGroup{JID: h.CanonicalJID, Name: h.ChatName})
		}
		data.Groups[i].Results = append(data.Groups[i].Results, searchResult{SearchHit: h, Time: view.FormatUnix(loc, h.TS), Segments: highlightSearch(h.Text, data.Query)})
	}
	data.ResultCount, data.ChatCount = len(hits), len(data.Groups)
	if len(hits) == limit {
		last := hits[len(hits)-1]
		next := url.Values{}
		for key, values := range q {
			if key == "before" || key == "before_chat" || key == "before_id" {
				continue
			}
			for _, value := range values {
				next.Add(key, value)
			}
		}
		next.Set("before", strconv.FormatInt(last.TS, 10))
		next.Set("before_chat", last.CanonicalJID)
		next.Set("before_id", last.ID)
		data.HasMore, data.NextURL = true, "/search?"+next.Encode()
	}
	s.render(w, s.pages["search"], "layout", data)
}
