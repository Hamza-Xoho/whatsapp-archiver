package main

import (
	"errors"
	"strings"
	"testing"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/backup"
)

func TestBackupResultIsPublishedAndPassedOn(t *testing.T) {
	bus := activity.NewBus(500)
	var passed int
	on := publishBackup(bus, func(backup.Report, error) { passed++ })
	on(backup.Report{Snapshot: "20261020T003000", Bytes: 2048}, nil)
	on(backup.Report{}, errors.New("open /mnt/data/backups/x: secret path"))
	got := bus.Recent()
	if passed != 2 || len(got) != 2 {
		t.Fatalf("passed=%d events=%+v", passed, got)
	}
	if got[0].Kind != "backup" || got[0].Detail != "snapshot done · 2048 bytes" {
		t.Fatalf("ok event=%+v", got[0])
	}
	if got[1].Kind != "backup" || got[1].Detail != "failed" || strings.Contains(got[1].Detail, "secret") {
		t.Fatalf("failed event=%+v", got[1])
	}
}
