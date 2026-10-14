package wa

import (
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// action is what the reconnect loop does next, in field order: disconnect, wait,
// then either refresh the version (its result is fed back) or connect.
type action struct {
	disconnect bool
	wait       time.Duration
	refresh    bool
	connect    bool
	stop       bool
}

// Loop-generated inputs; the others are whatsmeow events.
type (
	startInput       struct{}
	connectFailed    struct{}
	versionRefreshed struct{ changed bool }
)

const (
	connectWaitMin  = 5 * time.Second
	connectWaitMax  = 5 * time.Minute
	outdatedWait    = 30 * time.Minute
	banMargin       = time.Minute
	banUnknownWait  = 24 * time.Hour
	replaceWaitMin  = 5 * time.Minute
	replaceWaitMax  = time.Hour
	replaceQuietGap = 6 * time.Hour
	failureWait     = 30 * time.Minute
)

// policy is the pure reconnect state machine: input in, action out. No I/O, no sleeping.
type policy struct {
	connectWait    time.Duration
	replaceWait    time.Duration
	lastReplace    time.Time
	outdatedWaited bool
}

func (p *policy) next(in any, now time.Time) action {
	switch e := in.(type) {
	case startInput:
		return action{connect: true}
	case connectFailed:
		p.connectWait = doubled(p.connectWait, connectWaitMin, connectWaitMax)
		return action{wait: p.connectWait, connect: true}
	case *events.Connected:
		p.connectWait = 0
	case *events.ClientOutdated:
		p.outdatedWaited = false
		return action{disconnect: true, refresh: true}
	case versionRefreshed:
		if e.changed || p.outdatedWaited {
			p.outdatedWaited = false
			return action{connect: true}
		}
		p.outdatedWaited = true
		return action{wait: outdatedWait, refresh: true}
	case *events.TemporaryBan:
		wait := e.Expire + banMargin
		if e.Expire == 0 {
			wait = banUnknownWait
		}
		return action{disconnect: true, wait: wait, connect: true}
	case *events.ConnectFailure, *events.CATRefreshError:
		return action{disconnect: true, wait: failureWait, connect: true}
	case *events.LoggedOut:
		return action{stop: true}
	case *events.StreamReplaced:
		if p.lastReplace.IsZero() || now.Sub(p.lastReplace) > replaceQuietGap {
			p.replaceWait = 0
		}
		p.replaceWait = doubled(p.replaceWait, replaceWaitMin, replaceWaitMax)
		p.lastReplace = now
		return action{disconnect: true, wait: p.replaceWait, connect: true}
	}
	return action{}
}

func doubled(d, lo, hi time.Duration) time.Duration {
	return min(max(2*d, lo), hi)
}
