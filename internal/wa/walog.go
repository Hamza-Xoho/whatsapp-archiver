package wa

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// waLogger adapts whatsmeow's waLog.Logger to slog; Sub nests the module attribute.
type waLogger struct {
	log    *slog.Logger
	module string
}

func newWALogger(log *slog.Logger) waLog.Logger { return waLogger{log: log} }

func (l waLogger) Errorf(msg string, args ...any) { l.logf(slog.LevelError, msg, args) }
func (l waLogger) Warnf(msg string, args ...any)  { l.logf(slog.LevelWarn, msg, args) }
func (l waLogger) Infof(msg string, args ...any)  { l.logf(slog.LevelInfo, msg, args) }
func (l waLogger) Debugf(msg string, args ...any) { l.logf(slog.LevelDebug, msg, args) }

func (l waLogger) Sub(module string) waLog.Logger {
	if l.module != "" {
		module = l.module + "/" + module
	}
	return waLogger{log: l.log, module: module}
}

// benignWarnPrefixes are whatsmeow WARN lines we have judged harmless. Only these are demoted.
var benignWarnPrefixes = []string{"Set status notification has unexpected content"}

func (l waLogger) logf(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	text := fmt.Sprintf(msg, args...)
	if level == slog.LevelWarn && hasAnyPrefix(text, benignWarnPrefixes) {
		level = slog.LevelDebug
	}
	if !l.log.Enabled(ctx, level) {
		return
	}
	var attrs []slog.Attr
	if l.module != "" {
		attrs = append(attrs, slog.String("module", l.module))
	}
	l.log.LogAttrs(ctx, level, text, attrs...)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
