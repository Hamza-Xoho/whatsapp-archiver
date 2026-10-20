package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/view"
)

// recentEvents is how many activity events the dashboard lists on load; the live stream adds the rest.
const recentEvents = 20

// feedItem is one activity line. Name is the chat's name when the archive knows it; the JID stays as secondary text.
// The live stream sends items without a name (a name lookup per event would add a read for every captured message).
type feedItem struct {
	activity.Event
	Name string
}

// healthView is the connection panel, in the words of the user's side of the screen.
type healthView struct {
	State, StateClass, Detail string
	Version, LastEvent        string
	PhoneLastActive           string
	PhoneQuiet                bool
	PhoneQuietFor             string
	Panics                    int
}

type dashboardData struct {
	page
	Health                      healthView
	HasArchive                  bool
	History                     string
	Logging, NotLogging, Gaps   int
	LoggingNoun, NotLoggingNoun string
	Events                      []feedItem
}

// dashboard is GET /. Like every GET it only reads.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	data := dashboardData{page: page{Title: "Dashboard"}, Health: s.healthView(ctx, loc)}
	names := map[string]string{}
	if s.d.Store != nil {
		report, err := s.d.Store.StatusReport(ctx)
		if err != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
		}
		filter := ingest.NewFilter(s.d.Rules, func(jid string) ([]string, error) { return s.d.Store.Aliases(ctx, jid) })
		for _, c := range report.Chats {
			names[c.CanonicalJID] = c.Name
			off, err := filter.Excluded(c.CanonicalJID)
			if err != nil {
				http.Error(w, "the archive could not be read", http.StatusInternalServerError)
				return
			}
			if off {
				data.NotLogging++
			} else {
				data.Logging++
			}
		}
		if data.Gaps, err = s.d.Store.OpenGapCount(ctx); err != nil {
			http.Error(w, "the archive could not be read", http.StatusInternalServerError)
			return
		}
		data.HasArchive = true
		data.History = view.HistoryLine(loc, report.Coverage)
		data.LoggingNoun, data.NotLoggingNoun = chatNoun(data.Logging), chatNoun(data.NotLogging)
	}
	data.Events = s.recent(names)
	s.render(w, s.pages["dashboard"], "layout", data)
}

// healthPartial is GET /partials/health, the panel alone; the page re-fetches it on each health event.
func (s *Server) healthPartial(w http.ResponseWriter, r *http.Request) {
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["dashboard"], "health_panel", s.healthView(r.Context(), loc))
}

// recent is the newest recentEvents events, newest first.
func (s *Server) recent(names map[string]string) []feedItem {
	all := s.d.Activity.Recent() // oldest first; a nil bus has none
	if len(all) > recentEvents {
		all = all[len(all)-recentEvents:]
	}
	out := make([]feedItem, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, feedItem{Event: all[i], Name: names[all[i].Chat]})
	}
	return out
}

func (s *Server) healthView(ctx context.Context, loc *time.Location) healthView {
	var snap health.Snapshot
	if s.d.Health != nil {
		snap = s.d.Health()
	}
	v := healthView{Detail: snap.Detail, Version: "-", Panics: snap.Panics,
		LastEvent: view.FormatTime(loc, snap.LastEvent), PhoneLastActive: view.FormatTime(loc, snap.PhoneLastActive)}
	v.State, v.StateClass = stateWords(snap.State)
	if snap.Version != "" {
		v.Version = snap.Version
		if snap.VersionReason != "" {
			v.Version += " (" + snap.VersionReason + ")"
		}
	}
	// An unknown phone time is not a warning: the monitor counts quiet from its own start, the page has no start.
	if limit := s.phoneQuietAfter(ctx); limit > 0 && !snap.PhoneLastActive.IsZero() {
		if quiet := s.d.Now().Sub(snap.PhoneLastActive); quiet > limit {
			v.PhoneQuiet, v.PhoneQuietFor = true, sinceWords(quiet)
		}
	}
	return v
}

// phoneQuietAfter is the live alert threshold (the alerts.thresholds setting, read at each use), falling back to
// config.yaml, the same order the monitor uses.
func (s *Server) phoneQuietAfter(ctx context.Context) time.Duration {
	if s.d.Store != nil {
		if a, ok, err := s.d.Store.AlertThresholds(ctx); err == nil && ok {
			return a.PhoneQuietAfter
		}
	}
	return s.config().Alerts.PhoneQuietAfter
}

// config is Deps.Config, or an empty one for an unwired server.
func (s *Server) config() *config.Config {
	if s.d.Config == nil {
		return &config.Config{}
	}
	return s.d.Config
}

func stateWords(state string) (label, class string) {
	switch state {
	case "connected":
		return "Connected", "ok"
	case "disconnected":
		return "Disconnected", "bad"
	case "", "unknown":
		return "Unknown", "warn"
	}
	return strings.ToUpper(state[:1]) + state[1:], "warn"
}

func sinceWords(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%d hours", int(d/time.Hour))
}

func chatNoun(n int) string {
	if n == 1 {
		return "chat"
	}
	return "chats"
}
