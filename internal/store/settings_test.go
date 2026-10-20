package store_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

// FR-18: a bulk change is one transaction. Nine valid rules followed by an invalid tenth leave no rule behind.
func TestSetChatRulesBulkIsOneTransaction(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	rules := make([]store.ChatRule, 10)
	for i := range 9 {
		rules[i] = store.ChatRule{JID: fmt.Sprintf("%d@s.whatsapp.net", i+1), Logging: false}
	}
	rules[9] = store.ChatRule{JID: "", Logging: false} // a rule without a chat is rejected
	if err := s.SetChatRules(ctx, rules); err == nil {
		t.Fatal("SetChatRules accepted a rule without a chat")
	}
	got, err := s.ChatRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a failed bulk change left %d rules: %+v", len(got), got)
	}
	if err = s.SetChatRules(ctx, rules[:9]); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ChatRules(ctx); len(got) != 9 {
		t.Fatalf("rules = %d, want 9", len(got))
	}
}

func TestChatRuleKeepsItsMediaRules(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	media := &config.MediaRules{Download: []string{}, VideoMaxMB: 5}
	if err := s.SetChatRules(ctx, []store.ChatRule{{JID: "g@g.us", Logging: true, Media: media}, {JID: "1@lid", Logging: false}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ChatRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byJID := map[string]store.ChatRule{}
	for _, r := range got {
		byJID[r.JID] = r
	}
	g, l := byJID["g@g.us"], byJID["1@lid"]
	if !g.Logging || g.Media == nil || g.Media.VideoMaxMB != 5 || g.Media.Download == nil || len(g.Media.Download) != 0 || g.UpdatedAt == 0 {
		t.Fatalf("group rule = %+v (media %+v)", g, g.Media)
	}
	if l.Logging || l.Media != nil {
		t.Fatalf("lid rule = %+v", l)
	}
}

// Review Focus: the first start of this build on the live archive imports config.yaml's exclusions once. A later
// start never imports again, so a chat turned back on in the UI stays on.
func TestConfigExclusionsImportedOnce(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	cfg := &config.Config{
		Exclude: []string{"9665@s.whatsapp.net", "120363@g.us"},
		Media:   config.MediaRules{Download: []string{"image"}, VideoMaxMB: 50, Skip: []string{"sticker"}, Workers: 2, RetryRequestsPerMinute: 10},
		Backup:  config.BackupConfig{Destination: "/mnt/backups", Time: "04:15", Keep: 9},
		Alerts: config.AlertConfig{Ntfy: &config.NtfyConfig{URL: "https://ntfy.example", Token: "tk_secret"}, NoEventsAfter: 2 * time.Hour,
			DisconnectedAfter: 20 * time.Minute, PhoneQuietAfter: 100 * time.Hour, DiskFreeMinGB: 7},
	}
	imported, err := s.ImportConfigOnce(ctx, cfg)
	if err != nil || !imported {
		t.Fatalf("first import: imported=%v err=%v", imported, err)
	}
	if imported, err = s.ImportConfigOnce(ctx, cfg); err != nil || imported {
		t.Fatalf("second import: imported=%v err=%v", imported, err)
	}
	rules, err := s.ChatRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var excluded []string
	for _, r := range rules {
		if !r.Logging {
			excluded = append(excluded, r.JID)
		}
	}
	slices.Sort(excluded)
	if len(rules) != 2 || !slices.Equal(excluded, []string{"120363@g.us", "9665@s.whatsapp.net"}) {
		t.Fatalf("rules after two imports = %+v", rules)
	}
	if err = s.SetChatRules(ctx, []store.ChatRule{{JID: "9665@s.whatsapp.net", Logging: true}}); err != nil {
		t.Fatal(err)
	}
	if imported, err = s.ImportConfigOnce(ctx, cfg); err != nil || imported {
		t.Fatalf("import after a UI change: imported=%v err=%v", imported, err)
	}
	rules, _ = s.ChatRules(ctx)
	for _, r := range rules {
		if r.JID == "9665@s.whatsapp.net" && !r.Logging {
			t.Fatal("a later import turned a re-enabled chat off again")
		}
	}

	media, ok, err := s.MediaRules(ctx)
	if err != nil || !ok || media.VideoMaxMB != 50 || !slices.Equal(media.Download, []string{"image"}) {
		t.Fatalf("media.rules = %+v ok=%v err=%v", media, ok, err)
	}
	alerts, ok, err := s.AlertThresholds(ctx)
	if err != nil || !ok || alerts.NoEventsAfter != 2*time.Hour || alerts.DisconnectedAfter != 20*time.Minute ||
		alerts.PhoneQuietAfter != 100*time.Hour || alerts.DiskFreeMinGB != 7 || alerts.Ntfy != nil {
		t.Fatalf("alerts.thresholds = %+v ok=%v err=%v", alerts, ok, err)
	}
	sched, ok, err := s.BackupSchedule(ctx)
	if err != nil || !ok || sched.Time != "04:15" || sched.Keep != 9 || sched.Destination != "" {
		t.Fatalf("backup.schedule = %+v ok=%v err=%v", sched, ok, err)
	}
	raw, ok, err := s.Setting(ctx, "alerts.thresholds")
	if err != nil || !ok {
		t.Fatalf("raw alerts.thresholds ok=%v err=%v", ok, err)
	}
	for _, field := range []string{"no_events_after", "disconnected_after", "phone_quiet_after", "disk_free_min_gb"} {
		if !strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("alerts.thresholds %s lacks %s", raw, field)
		}
	}
	if strings.Contains(raw, "tk_secret") || strings.Contains(raw, "ntfy") {
		t.Errorf("alerts.thresholds carries the ntfy settings: %s", raw)
	}
	if raw, _, _ = s.Setting(ctx, "backup.schedule"); !strings.Contains(raw, `"time"`) || !strings.Contains(raw, `"keep"`) || strings.Contains(raw, "/mnt/backups") {
		t.Errorf("backup.schedule = %s", raw)
	}
	if _, ok, _ = s.Setting(ctx, config.ImportMarker); !ok {
		t.Error("import marker not set")
	}
}

// Chats already in the archive when the import runs are not new: only chats first seen afterwards are flagged.
func TestImportMarksExistingChatsSeen(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.UpsertChat(ctx, store.Chat{JID: "old@g.us", Type: "group"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportConfigOnce(ctx, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if isNew, err := s.MarkChatSeen(ctx, "old@g.us", 5); err != nil || isNew {
		t.Fatalf("existing chat: isNew=%v err=%v", isNew, err)
	}
	if isNew, err := s.MarkChatSeen(ctx, "fresh@g.us", 5); err != nil || !isNew {
		t.Fatalf("new chat: isNew=%v err=%v", isNew, err)
	}
}

func TestMarkChatSeenReportsNewOnce(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if isNew, err := s.MarkChatSeen(ctx, "g@g.us", 100); err != nil || !isNew {
		t.Fatalf("first sighting: isNew=%v err=%v", isNew, err)
	}
	if isNew, err := s.MarkChatSeen(ctx, "g@g.us", 200); err != nil || isNew {
		t.Fatalf("second sighting: isNew=%v err=%v", isNew, err)
	}
	seen, err := s.SeenChats(ctx)
	if err != nil || len(seen) != 1 || seen[0].JID != "g@g.us" || seen[0].FirstSeen != 100 || seen[0].Acknowledged {
		t.Fatalf("seen = %+v err=%v", seen, err)
	}
	if err = s.AcknowledgeChats(ctx, []string{"g@g.us"}); err != nil {
		t.Fatal(err)
	}
	if seen, _ = s.SeenChats(ctx); len(seen) != 1 || !seen[0].Acknowledged || seen[0].FirstSeen != 100 {
		t.Fatalf("after acknowledge: %+v", seen)
	}
}

func TestSettingRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, ok, err := s.Setting(ctx, "media.rules"); ok || err != nil {
		t.Fatalf("unset setting: ok=%v err=%v", ok, err)
	}
	if err := s.SetMediaRules(ctx, config.MediaRules{Download: []string{"audio"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaRules(ctx, config.MediaRules{Download: []string{"document"}}); err != nil {
		t.Fatal(err)
	}
	if r, ok, err := s.MediaRules(ctx); err != nil || !ok || !slices.Equal(r.Download, []string{"document"}) {
		t.Fatalf("media rules = %+v ok=%v err=%v", r, ok, err)
	}
}
