package guard

import (
	"log/slog"
	"runtime/debug"
)

func Run(log *slog.Logger, where string, onPanic func(string, any), fn func()) {
	defer func() {
		if value := recover(); value != nil {
			log.Error("recovered panic", "where", where, "panic", value, "stack", string(debug.Stack()))
			if onPanic != nil {
				onPanic(where, value)
			}
		}
	}()
	fn()
}
