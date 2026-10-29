package wa

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsapp-archiver/internal/logsafe"
)

// waLogger adapts whatsmeow's waLog.Logger to slog; Sub nests the module attribute.
type waLogger struct {
	log    *slog.Logger
	module string
	repeat *repeatCollapser
}

type repeatKey struct {
	level  slog.Level
	module string
	text   string
}

type repeatWindow struct {
	started time.Time
	count   int
}

type repeatCollapser struct {
	mu      sync.Mutex
	now     func() time.Time
	windows map[repeatKey]repeatWindow
}

func newWALogger(log *slog.Logger) waLog.Logger { return newWALoggerAt(log, time.Now) }

func newWALoggerAt(log *slog.Logger, now func() time.Time) waLog.Logger {
	return waLogger{log: log, repeat: &repeatCollapser{now: now, windows: make(map[repeatKey]repeatWindow)}}
}

func (l waLogger) Errorf(msg string, args ...any) { l.logf(slog.LevelError, msg, args) }
func (l waLogger) Warnf(msg string, args ...any)  { l.logf(slog.LevelWarn, msg, args) }
func (l waLogger) Infof(msg string, args ...any)  { l.logf(slog.LevelInfo, msg, args) }
func (l waLogger) Debugf(msg string, args ...any) { l.logf(slog.LevelDebug, msg, args) }

func (l waLogger) Sub(module string) waLog.Logger {
	if l.module != "" {
		module = l.module + "/" + module
	}
	return waLogger{log: l.log, module: module, repeat: l.repeat}
}

// benignWarnPrefixes are whatsmeow WARN lines we have judged harmless. Only these are demoted.
var benignWarnPrefixes = []string{"Set status notification has unexpected content"}

func (l waLogger) logf(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	text := logsafe.RedactURLs(fmt.Sprintf(msg, args...))
	if level == slog.LevelWarn && hasAnyPrefix(text, benignWarnPrefixes) {
		level = slog.LevelDebug
	}
	if !l.log.Enabled(ctx, level) {
		return
	}
	key := repeatKey{level: level, module: l.module, text: text}
	emit, summary := l.repeat.line(key)
	if !emit {
		return
	}
	if summary > 0 {
		text = fmt.Sprintf("repeated %d times in 5 m", summary)
	}
	var attrs []slog.Attr
	if l.module != "" {
		attrs = append(attrs, slog.String("module", l.module))
	}
	l.log.LogAttrs(ctx, level, text, attrs...)
}

func (c *repeatCollapser) line(key repeatKey) (emit bool, summary int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	w, ok := c.windows[key]
	if !ok {
		c.windows[key] = repeatWindow{started: now}
		return true, 0
	}
	w.count++
	if now.Sub(w.started) < 5*time.Minute {
		c.windows[key] = w
		return false, 0
	}
	c.windows[key] = repeatWindow{started: now}
	return true, w.count
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
