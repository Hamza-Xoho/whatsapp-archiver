package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"whatsapp-archiver/internal/store"
	"whatsapp-archiver/internal/view"
)

type loggingSectionData struct {
	page
	Section, Heading, Retry string
	Error                   bool
	Outages                 []loggingOutageView
	Gaps                    []loggingGapView
	MediaStates             []store.LoggingCount
	MediaClasses            []store.LoggingCount
	MediaReasons            []store.LoggingMediaReason
	Media24, Media7         string
	HistorySyncs            []loggingSyncView
	SpoolBacklog            int
	SpoolErrors             int
	HistoryChats            []loggingHistoryChatView
	Alerts                  []loggingAlertView
	Storage                 storageData
	Backups                 backupsData
	Feed                    []feedItem
	FeedChat, FeedKind      string
	Settings                settingsData
}

type loggingOutageView struct {
	State, Started, Ended, Duration, Cause string
	Errors, Preview, Completed             int
}

type loggingGapView struct {
	CanonicalJID, TargetID, Cause, Age string
	PostOutage                         bool
}

type loggingSyncView struct {
	store.SyncCoverage
	Stalled bool
}

type loggingHistoryChatView struct {
	CanonicalJID, Name, Oldest string
}

type loggingAlertView struct {
	Title, Kind, State, Raised, Delivery string
	Attempts                             int
}

func (s *Server) loggingSection(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	if !validLoggingSection(section) {
		http.NotFound(w, r)
		return
	}
	data, err := s.loggingSectionData(r, section)
	if err != nil {
		data = loggingSectionData{
			page: page{Title: "Logging", Current: "Logging"}, Section: section,
			Heading: loggingHeading(section), Retry: "/logging/" + section, Error: true,
		}
		w.WriteHeader(http.StatusInternalServerError)
	}
	s.render(w, s.pages["logging_section"], "layout", data)
}

func validLoggingSection(section string) bool {
	switch section {
	case "outages", "gaps-media", "history", "alerts", "storage", "feed", "settings":
		return true
	default:
		return false
	}
}

func loggingHeading(section string) string {
	switch section {
	case "gaps-media":
		return "Gaps & media"
	case "storage":
		return "Storage & backups"
	case "feed":
		return "Live feed"
	default:
		return titleWords(section)
	}
}

func (s *Server) loggingSectionData(r *http.Request, section string) (loggingSectionData, error) {
	loc, err := view.Location(s.config())
	if err != nil {
		return loggingSectionData{}, err
	}
	now := s.d.Now()
	data := loggingSectionData{page: page{Title: "Logging", Current: "Logging", Live: section == "feed"}, Section: section,
		FeedChat: strings.TrimSpace(r.URL.Query().Get("chat")), FeedKind: strings.TrimSpace(r.URL.Query().Get("kind"))}
	if section == "feed" {
		query := url.Values{}
		if data.FeedChat != "" {
			query.Set("chat", data.FeedChat)
		}
		if data.FeedKind != "" {
			query.Set("kind", data.FeedKind)
		}
		data.EventsURL = "/events"
		if encoded := query.Encode(); encoded != "" {
			data.EventsURL += "?" + encoded
		}
	}
	if s.d.Store == nil {
		if section == "feed" {
			data.Feed = s.recentFiltered(nil, loc, data.FeedChat, data.FeedKind)
		}
		return data, nil
	}
	switch section {
	case "outages":
		rows, readErr := s.d.Store.LoggingOutages(r.Context(), 200)
		if readErr != nil {
			return data, readErr
		}
		for _, row := range rows {
			end, duration := "In progress", now.Sub(time.Unix(row.StartedAt, 0))
			if row.EndedAt > 0 {
				end, duration = view.FormatUnix(loc, row.EndedAt), time.Unix(row.EndedAt, 0).Sub(time.Unix(row.StartedAt, 0))
			}
			data.Outages = append(data.Outages, loggingOutageView{State: titleWords(row.State), Started: view.FormatUnix(loc, row.StartedAt),
				Ended: end, Duration: durationWords(duration), Cause: causeWords(row.CauseClass), Errors: row.ErrorCount,
				Preview: optionalCount(row.OfflinePreviewMsgs), Completed: optionalCount(row.OfflineCompletedCount)})
		}
	case "gaps-media":
		gaps, readErr := s.d.Store.LoggingGaps(r.Context(), 200)
		if readErr != nil {
			return data, readErr
		}
		for _, gap := range gaps {
			data.Gaps = append(data.Gaps, loggingGapView{CanonicalJID: gap.CanonicalJID, TargetID: gap.TargetID,
				Cause: gapCauseWords(gap.Cause), Age: agoWords(now.Sub(time.Unix(gap.ReceivedAt, 0))), PostOutage: gap.PostOutage})
		}
		media, readErr := s.d.Store.LoggingMedia(r.Context(), 50)
		if readErr != nil {
			return data, readErr
		}
		data.MediaStates = labelledCounts(media.States)
		data.MediaClasses = labelledCounts(media.ErrorClasses)
		data.MediaReasons = safeMediaReasons(media.Reasons)
		timeliness, readErr := s.d.Store.MediaTimeliness(r.Context(), now)
		if readErr != nil {
			return data, readErr
		}
		data.Media24 = view.PercentageCount(timeliness.DayDone, timeliness.DayTotal)
		data.Media7 = view.PercentageCount(timeliness.WeekDone, timeliness.WeekTotal)
	case "history":
		history, readErr := s.d.Store.LoggingHistory(r.Context(), 250)
		if readErr != nil {
			return data, readErr
		}
		for _, sync := range history.Syncs {
			data.HistorySyncs = append(data.HistorySyncs, loggingSyncView{SyncCoverage: sync,
				Stalled: !sync.Complete && sync.LastChunkTime > 0 && !now.Before(time.Unix(sync.LastChunkTime, 0).Add(48*time.Hour))})
		}
		data.SpoolBacklog, data.SpoolErrors = history.SpoolBacklog, history.SpoolErrors
		for _, chat := range history.Chats {
			data.HistoryChats = append(data.HistoryChats, loggingHistoryChatView{CanonicalJID: chat.CanonicalJID,
				Name: chat.Name, Oldest: view.FormatUnix(loc, chat.OldestHistory)})
		}
	case "alerts":
		alerts, readErr := s.d.Store.LoggingAlerts(r.Context(), 200)
		if readErr != nil {
			return data, readErr
		}
		for _, alert := range alerts {
			delivery := ""
			if alert.SentAt > 0 {
				delivery = "Delivered"
				if late := time.Duration(alert.SentAt-alert.RaisedAt) * time.Second; late >= time.Minute {
					delivery += " " + durationWords(late) + " late"
				}
			}
			data.Alerts = append(data.Alerts, loggingAlertView{Title: alert.Title, Kind: titleWords(alert.Kind),
				State: titleWords(alert.State), Raised: view.FormatUnix(loc, alert.RaisedAt), Delivery: delivery, Attempts: alert.Attempts})
		}
	case "storage":
		facts, readErr := s.storageCache.get(now, func() (storageFacts, error) { return s.storageFacts(r.Context(), now, loc) })
		if readErr != nil {
			return data, readErr
		}
		data.Storage = facts.storageData
		data.Backups, readErr = s.backupsData(r)
		if readErr != nil {
			return data, readErr
		}
	case "feed":
		names, readErr := s.loggingFeedNames(r.Context())
		if readErr != nil {
			return data, readErr
		}
		data.Feed = s.recentFiltered(names, loc, data.FeedChat, data.FeedKind)
	case "settings":
		settings, readErr := s.settingsData(r.Context())
		if readErr != nil {
			return data, readErr
		}
		data.Settings = settings
	}
	return data, nil
}

func safeMediaReasons(rows []store.LoggingMediaReason) []store.LoggingMediaReason {
	positions := make(map[string]int)
	var safe []store.LoggingMediaReason
	for _, row := range rows {
		name := safeMediaReason(row.Reason)
		if at, ok := positions[name]; ok {
			safe[at].Count += row.Count
			continue
		}
		positions[name] = len(safe)
		safe = append(safe, store.LoggingMediaReason{Reason: name, Count: row.Count})
	}
	return safe
}

func safeMediaReason(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "dns"):
		return "DNS lookup failed"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"):
		return "Request timed out"
	case strings.Contains(lower, "403"), strings.Contains(lower, "expired"):
		return "Media expired"
	case strings.Contains(lower, "404"), strings.Contains(lower, "410"), strings.Contains(lower, "gone"):
		return "Media unavailable"
	case strings.Contains(lower, "key"):
		return "Media key unavailable"
	case strings.Contains(lower, "re-upload"), strings.Contains(lower, "reupload"):
		return "Re-upload unavailable"
	case strings.Contains(lower, "network"), strings.Contains(lower, "connection"):
		return "Network request failed"
	default:
		return "Download failed"
	}
}

func (s *Server) feedPartial(w http.ResponseWriter, r *http.Request) {
	loc, err := view.Location(s.config())
	if err != nil {
		http.Error(w, "The archive could not be read. Try again.", http.StatusInternalServerError)
		return
	}
	data := loggingSectionData{FeedChat: strings.TrimSpace(r.URL.Query().Get("chat")), FeedKind: strings.TrimSpace(r.URL.Query().Get("kind"))}
	names, err := s.loggingFeedNames(r.Context())
	if err != nil {
		http.Error(w, "The archive could not be read. Try again.", http.StatusInternalServerError)
		return
	}
	data.Feed = s.recentFiltered(names, loc, data.FeedChat, data.FeedKind)
	s.render(w, s.pages["logging_section"], "feed_panel", data)
}

func (s *Server) loggingFeedNames(ctx context.Context) (map[string]string, error) {
	names := map[string]string{}
	if s.d.Store == nil {
		return names, nil
	}
	controls, err := s.d.Store.ChatControls(ctx)
	if err != nil {
		return nil, err
	}
	for jid, control := range controls {
		names[jid] = control.Name
	}
	return names, nil
}

func (s *Server) recentFiltered(names map[string]string, loc *time.Location, chat, kind string) []feedItem {
	rows := s.recent(names, loc)
	if chat == "" && kind == "" {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		if chat != "" && row.Chat != chat {
			continue
		}
		if kind != "" && row.Kind != kind {
			continue
		}
		out = append(out, row)
	}
	return out
}

func labelledCounts(rows []store.LoggingCount) []store.LoggingCount {
	out := make([]store.LoggingCount, len(rows))
	for i, row := range rows {
		out[i] = store.LoggingCount{Name: titleWords(row.Name), Count: row.Count}
	}
	return out
}

func titleWords(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func causeWords(value string) string {
	if strings.EqualFold(value, "dns") {
		return "DNS"
	}
	return titleWords(value)
}

func optionalCount(value *int64) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func durationWords(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Hour {
		hours := int(d / time.Hour)
		return fmt.Sprintf("%d %s", hours, plural(hours, "hour", "hours"))
	}
	minutes := int(d / time.Minute)
	return fmt.Sprintf("%d %s", minutes, plural(minutes, "minute", "minutes"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
