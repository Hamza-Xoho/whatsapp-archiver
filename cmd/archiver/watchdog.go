package main

import (
	"context"
	"time"
)

const (
	watchdogInterval = 60 * time.Second
	watchdogMaxStall = 120 * time.Second
)

type aliveWriter interface {
	RecordAlive(context.Context, int64) error
}

type serviceWatchdog struct {
	writer   aliveWriter
	handlers *inFlight
	now      func() time.Time
	notify   func(string) error
}

func (w serviceWatchdog) tick(ctx context.Context) error {
	now := w.now()
	if err := w.writer.RecordAlive(ctx, now.Unix()); err != nil {
		return err
	}
	if !w.handlers.progressing(now, watchdogMaxStall) {
		return nil
	}
	return w.notify("WATCHDOG=1")
}

func (w serviceWatchdog) run(ctx context.Context) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.tick(ctx)
		}
	}
}
