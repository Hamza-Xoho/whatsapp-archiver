package wa

import (
	"context"
	"fmt"
	"log/slog"

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

func (l waLogger) logf(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	if !l.log.Enabled(ctx, level) {
		return
	}
	var attrs []slog.Attr
	if l.module != "" {
		attrs = append(attrs, slog.String("module", l.module))
	}
	l.log.LogAttrs(ctx, level, fmt.Sprintf(msg, args...), attrs...)
}
