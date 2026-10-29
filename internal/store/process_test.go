package store_test

import (
	"testing"
)

func TestProcessHeartbeatRecordsDowntimeAndWriterProgress(t *testing.T) {
	s := open(t)
	if err := s.SetSetting(t.Context(), "last_alive_at", "100"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProcessStart(t.Context(), 200); err != nil {
		t.Fatal(err)
	}
	periods, err := s.ConnectionPeriods(t.Context(), 0)
	if err != nil || len(periods) != 1 || periods[0].State != "not_running" || periods[0].CauseClass != "not_running" || periods[0].StartedAt != 100 || periods[0].EndedAt != 200 {
		t.Fatalf("periods=%+v err=%v", periods, err)
	}
	if err := s.RecordAlive(t.Context(), 260); err != nil {
		t.Fatal(err)
	}
	if got := text(t, s, "SELECT value FROM settings WHERE key='last_alive_at'"); got != "260" {
		t.Fatalf("last_alive_at=%q", got)
	}
}
