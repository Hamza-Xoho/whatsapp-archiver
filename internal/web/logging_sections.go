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
	OutageDays              []store.OutageDay
	OutageToday             string
	OutageLongest           string
	Gaps                    []loggingGapView
	MediaStates             []store.LoggingCount
	MediaClasses            []store.LoggingCount
	MediaReasons            []store.LoggingMediaReason
	Media24, Media7         string
	MediaFailures           bool
	HistorySyncs            []loggingSyncView
	SpoolBacklog            int
	SpoolErrors             int
	HistoryChats            []loggingHistoryChatView
	HistoryTotal            int
	HistoryHasMore          bool
	HistoryNext             string
	Alerts                  []loggingAlertView
	Storage                 storageData
	Backups                 backupsData
	Feed                    []feedItem
	FeedChat, FeedKind      string
	Settings                settingsData
}

type loggingOutageView struct {
	State, Started, Ended, Duration, Cause string
	Errors                                 int
	Preview, Completed                     string
}

type loggingGapView struct {
	CanonicalJID, TargetID, Cause, Age, Oldest string
	PostOutage                                 bool
	Count                                      int
}

type loggingSyncView struct {
	store.SyncCoverage
	Stalled                bool
	OldestText, NewestText string
}

type loggingHistoryChatView struct {
	CanonicalJID, Name, State string
}

type loggingAlertView struct {
	Title, Kind, State, Raised, Delivered, Late string
	Attempts                                    int
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
	}
	if section == "settings" {
		switch saved := strings.SplitN(r.URL.Query().Get("saved"), "#", 2)[0]; saved {
		case "media", "alerts", "backup", "heartbeat":
			data.Settings.Saved = saved
		}
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
		summary, readErr := s.d.Store.OutageSummary(r.Context(), now, loc, 7)
		if readErr != nil {
			return data, readErr
		}
		data.OutageDays = summary.Days
		if len(summary.Days) > 0 {
			minutes := summary.Days[len(summary.Days)-1].Minutes
			data.OutageToday = fmt.Sprintf("%d %s", minutes, plural(minutes, "minute", "minutes"))
		}
		data.OutageLongest = durationWords(summary.Longest)
		for _, row := range rows {
			end, duration := "In progress", now.Sub(time.Unix(row.StartedAt, 0))
			if row.EndedAt > 0 {
				end, duration = view.FormatUnix(loc, row.EndedAt), time.Unix(row.EndedAt, 0).Sub(time.Unix(row.StartedAt, 0))
			}
			data.Outages = append(data.Outages, loggingOutageView{State: titleWords(row.State), Started: view.FormatUnix(loc, row.StartedAt),
				Ended: end, Duration: durationWords(duration), Cause: causeWords(row.CauseClass), Errors: row.ErrorCount,
				Preview: optionalCountWords(row.OfflinePreviewMsgs), Completed: optionalCountWords(row.OfflineCompletedCount)})
		}
	case "gaps-media":
		gaps, readErr := s.d.Store.LoggingGapGroups(r.Context())
		if readErr != nil {
			return data, readErr
		}
		for _, gap := range gaps {
			data.Gaps = append(data.Gaps, loggingGapView{CanonicalJID: gap.Representative.CanonicalJID,
				TargetID: gap.Representative.TargetID, Cause: gapCauseWords(gap.Cause),
				Age:    agoWords(now.Sub(time.Unix(gap.Representative.ReceivedAt, 0))),
				Oldest: agoWords(now.Sub(time.Unix(gap.OldestReceivedAt, 0))), PostOutage: gap.PostOutage, Count: gap.Count})
		}
		media, readErr := s.d.Store.LoggingMedia(r.Context(), 50)
		if readErr != nil {
			return data, readErr
		}
		data.MediaStates = labelledCounts(media.States)
		data.MediaClasses = labelledCounts(media.ErrorClasses)
		data.MediaReasons = media.Reasons
		data.MediaFailures = hasMediaFailures(media)
		timeliness, readErr := s.d.Store.MediaTimeliness(r.Context(), now)
		if readErr != nil {
			return data, readErr
		}
		data.Media24 = view.PercentageCount(timeliness.DayDone, timeliness.DayTotal)
		data.Media7 = view.PercentageCount(timeliness.WeekDone, timeliness.WeekTotal)
	case "history":
		history, readErr := s.d.Store.LoggingHistoryPage(r.Context(), 250, strings.TrimSpace(r.URL.Query().Get("after")))
		if readErr != nil {
			return data, readErr
		}
		for _, sync := range history.Syncs {
			data.HistorySyncs = append(data.HistorySyncs, loggingSyncView{SyncCoverage: sync,
				Stalled:    !sync.Complete && sync.LastChunkTime > 0 && !now.Before(time.Unix(sync.LastChunkTime, 0).Add(48*time.Hour)),
				OldestText: optionalUnix(loc, sync.Oldest), NewestText: optionalUnix(loc, sync.Newest)})
		}
		data.SpoolBacklog, data.SpoolErrors = history.SpoolBacklog, history.SpoolErrors
		data.HistoryTotal, data.HistoryHasMore = history.TotalChats, history.HasMore
		if history.HasMore {
			data.HistoryNext = "/logging/history?after=" + url.QueryEscape(history.NextAfter)
		}
		for _, chat := range history.Chats {
			state := "No history messages archived"
			if chat.HasHistory {
				state = "History received back to " + view.FormatUnix(loc, chat.OldestHistory)
				if history.AuthoritativeComplete {
					state = "History complete back to " + view.FormatUnix(loc, chat.OldestHistory)
				}
			}
			data.HistoryChats = append(data.HistoryChats, loggingHistoryChatView{CanonicalJID: chat.CanonicalJID,
				Name: chat.Name, State: state})
		}
	case "alerts":
		alerts, readErr := s.d.Store.LoggingAlerts(r.Context(), 200)
		if readErr != nil {
			return data, readErr
		}
		for _, alert := range alerts {
			delivered, lateText := "", ""
			if alert.SentAt > 0 {
				delivered = view.FormatUnix(loc, alert.SentAt)
				if late := time.Duration(alert.SentAt-alert.RaisedAt) * time.Second; late >= time.Minute {
					lateText = durationWords(late) + " late"
				}
			}
			data.Alerts = append(data.Alerts, loggingAlertView{Title: alert.Title, Kind: titleWords(alert.Kind),
				State: titleWords(alert.State), Raised: view.FormatUnix(loc, alert.RaisedAt), Delivered: delivered, Late: lateText, Attempts: alert.Attempts})
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
		switch saved := strings.SplitN(r.URL.Query().Get("saved"), "#", 2)[0]; saved {
		case "media", "alerts", "backup", "heartbeat":
			settings.Saved = saved
		}
		data.Settings = settings
	}
	return data, nil
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
	events := s.d.Activity.RecentFiltered(chat, kind, recentEvents)
	out := make([]feedItem, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		out = append(out, feedItem{Event: events[i], Name: names[events[i].Chat], Loc: loc})
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
