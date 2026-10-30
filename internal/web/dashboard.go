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
	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

// recentEvents is how many activity events the dashboard lists on load; the live stream adds the rest.
const recentEvents = 20

// feedItem is one activity line. Name is the chat's name when the archive knows it; the JID stays as secondary text.
// The live stream sends items without a name (a name lookup per event would add a read for every captured message).
type feedItem struct {
	activity.Event
	Name string
	Loc  *time.Location // the configured time zone; nil is UTC
}

func (f feedItem) at() time.Time {
	if f.Loc == nil {
		return f.At.UTC()
	}
	return f.At.In(f.Loc)
}

// Clock is the line's time of day in the configured zone; Stamp is the same instant for the time element.
func (f feedItem) Clock() string { return f.at().Format("15:04:05") }
func (f feedItem) Stamp() string { return f.at().Format("2006-01-02T15:04:05Z07:00") }

// healthView is the connection panel, in the words of the user's side of the screen.
type healthView struct {
	State, StateClass, Detail string
	Version, LastEvent        string
	PhoneLastActive           string
	SessionAge, LastEventAgo  string
	PhoneActiveAgo            string
	PhoneQuiet                bool
	PhoneQuietFor             string
	Panics                    int
}

type dashboardData struct {
	page
	Health                                     healthView
	HasArchive                                 bool
	History                                    []string
	Logging, NotLogging, Gaps                  int
	ArchiveChats, ArchiveMessages, FailedMedia int
	LoggingNoun, NotLoggingNoun                string
	GapGroups                                  []gapSummaryView
	Media24Hours, Media7Days                   string
	Events                                     []feedItem
	Outages                                    []store.OutageDay
	HasOutages                                 bool
	LongestOutageMinutes                       int
}

type gapSummaryView struct {
	Cause, GapNoun, Age string
	Count               int
	PostOutage          bool
}

// dashboard is GET /. Like every GET it only reads.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := s.dashboardData(r.Context())
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["dashboard"], "layout", data)
}

func (s *Server) dashboardData(ctx context.Context) (dashboardData, error) {
	loc, err := view.Location(s.config())
	if err != nil {
		return dashboardData{}, err
	}
	data := dashboardData{page: page{Title: "Logging", Current: "Logging", Live: true}, Health: s.healthView(ctx, loc)}
	names := map[string]string{}
	if s.d.Store != nil {
		report, err := s.d.Store.StatusReport(ctx)
		if err != nil {
			return dashboardData{}, err
		}
		media, err := s.d.Store.MediaTimeliness(ctx, s.d.Now())
		if err != nil {
			return dashboardData{}, err
		}
		data.Media24Hours = view.PercentageCount(media.DayDone, media.DayTotal)
		data.Media7Days = view.PercentageCount(media.WeekDone, media.WeekTotal)
		filter := ingest.NewFilter(s.d.Rules, func(jid string) ([]string, error) { return s.d.Store.Aliases(ctx, jid) })
		for _, c := range report.Chats {
			data.ArchiveChats++
			data.ArchiveMessages += c.MessageCount
			data.FailedMedia += c.MediaFailed
			names[c.CanonicalJID] = c.Name
			off, err := filter.Excluded(c.CanonicalJID)
			if err != nil {
				return dashboardData{}, err
			}
			if off {
				data.NotLogging++
			} else {
				data.Logging++
			}
		}
		gaps, err := s.d.Store.OpenGapSummary(ctx)
		if err != nil {
			return dashboardData{}, err
		}
		for _, gap := range gaps {
			data.Gaps += gap.Count
			data.GapGroups = append(data.GapGroups, gapSummaryView{Cause: gapCauseWords(gap.Cause), Count: gap.Count,
				GapNoun: gapNoun(gap.Count), Age: gapAge(s.d.Now(), gap.OldestReceivedAt), PostOutage: gap.PostOutage})
		}
		outages, err := s.d.Store.OutageSummary(ctx, s.d.Now(), loc, 7)
		if err != nil {
			return dashboardData{}, err
		}
		data.Outages, data.HasOutages = outages.Days, outages.HasOutages
		data.LongestOutageMinutes = roundedMinutes(outages.Longest)
		data.HasArchive = true
		data.History = view.HistoryLinesAt(loc, report.Coverage, s.d.Now())
		data.LoggingNoun, data.NotLoggingNoun = chatNoun(data.Logging), chatNoun(data.NotLogging)
	}
	return data, nil
}

func roundedMinutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Minute - 1) / time.Minute)
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

func (s *Server) inactivityPartial(w http.ResponseWriter, r *http.Request) {
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["logging"], "logging_inactivity", s.healthView(r.Context(), loc))
}

// recent is the newest recentEvents events, newest first.
func (s *Server) recent(names map[string]string, loc *time.Location) []feedItem {
	all := s.d.Activity.Recent() // oldest first; a nil bus has none
	if len(all) > recentEvents {
		all = all[len(all)-recentEvents:]
	}
	out := make([]feedItem, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, feedItem{Event: all[i], Name: names[all[i].Chat], Loc: loc})
	}
	return out
}

func (s *Server) healthView(ctx context.Context, loc *time.Location) healthView {
	var snap health.Snapshot
	if s.d.Health != nil {
		snap = s.d.Health()
	}
	v := healthView{Detail: safeHealthDetail(snap.State, snap.Detail), Version: "-", Panics: snap.Panics,
		LastEvent: view.FormatTime(loc, snap.LastEvent), PhoneLastActive: view.FormatTime(loc, snap.PhoneLastActive)}
	v.State, v.StateClass = stateWords(snap.State)
	if snap.Version != "" {
		v.Version = snap.Version
		if reason := safeVersionReason(snap.VersionReason); reason != "" {
			v.Version += " (" + reason + ")"
		}
	}
	if started, ok := s.d.Pairer.(interface{ PairingStarted() time.Time }); ok {
		if at := started.PairingStarted(); !at.IsZero() {
			v.SessionAge = sinceWords(max(s.d.Now().Sub(at), 0))
		}
	}
	if !snap.LastEvent.IsZero() {
		v.LastEventAgo = agoWords(s.d.Now().Sub(snap.LastEvent))
	}
	if !snap.PhoneLastActive.IsZero() {
		v.PhoneActiveAgo = agoWords(s.d.Now().Sub(snap.PhoneLastActive))
	}
	// An unknown phone time is not a warning: the monitor counts quiet from its own start, the page has no start.
	if limit := s.phoneQuietAfter(ctx); limit > 0 && !snap.PhoneLastActive.IsZero() {
		if quiet := s.d.Now().Sub(snap.PhoneLastActive); quiet > limit {
			v.PhoneQuiet, v.PhoneQuietFor = true, sinceWords(quiet)
		}
	}
	return v
}

func safeVersionReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case reason == "latest":
		return "latest"
	case reason == "built-in", reason == "builtin":
		return "built-in"
	case strings.Contains(reason, "override"):
		return "override"
	case strings.Contains(reason, "lookup"), strings.Contains(reason, "failed"), strings.Contains(reason, "error"):
		return "lookup failed"
	default:
		return ""
	}
}

// safeHealthDetail reduces transport-owned error text to controlled copy. A
// monitor detail can contain a URL, filesystem path or server response and is
// never suitable for direct HTML rendering.
func safeHealthDetail(state, detail string) string {
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" {
		return ""
	}
	if detail == "stream error" { // stable legacy monitor category
		return detail
	}
	cause := ""
	for _, item := range []struct{ needle, label string }{
		{"dns", "DNS"}, {"tls", "TLS"}, {"websocket", "WebSocket"}, {"class=ws", "WebSocket"},
		{"tcp", "network"}, {"keepalive", "keepalive"},
	} {
		if strings.Contains(detail, item.needle) {
			cause = item.label
			break
		}
	}
	switch state {
	case "disconnected", "reconnecting", "degraded":
		if cause != "" {
			return "Connection interrupted (" + cause + ")"
		}
		return "Connection interrupted"
	case "logged_out":
		return "WhatsApp logged out"
	case "client_outdated":
		return "WhatsApp client update required"
	case "temp_banned":
		return "WhatsApp temporarily paused capture"
	case "stream_replaced":
		return "WhatsApp session replaced"
	default:
		return ""
	}
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
	case "logged_out":
		return "Logged out", "bad"
	case "reconnecting":
		return "Reconnecting", "bad"
	case "degraded":
		return "Degraded", "bad"
	case "", "unknown":
		return "Unknown", "warn"
	}
	return strings.ToUpper(state[:1]) + state[1:], "warn"
}

func agoWords(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%d seconds ago", int(d/time.Second))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes ago", int(d/time.Minute))
	}
	return sinceWords(d) + " ago"
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

func gapNoun(n int) string {
	if n == 1 {
		return "gap"
	}
	return "gaps"
}

func gapCauseWords(cause string) string {
	switch cause {
	case "no_sender_key":
		return "No sender key"
	case "untrusted_identity":
		return "Untrusted identity"
	case "view_once_unavailable":
		return "View-once unavailable"
	case "other":
		return "Other"
	default:
		return "Unknown"
	}
}

func gapAge(now time.Time, receivedAt int64) string {
	age := now.Sub(time.Unix(receivedAt, 0))
	if age < time.Hour {
		return "less than an hour"
	}
	if age < 2*time.Hour {
		return "1 hour"
	}
	return sinceWords(age)
}
