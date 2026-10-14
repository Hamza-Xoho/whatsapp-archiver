package wa

import (
	"fmt"

	wmstore "go.mau.fi/whatsmeow/store"
)

func decideVersion(built, latest, override string, latestErr error, checkLatest bool) (string, string) {
	if override != "" {
		return override, "override"
	}
	if !checkLatest {
		return built, "built-in"
	}
	if latestErr != nil {
		return built, "built-in: " + latestErr.Error()
	}
	b, _ := wmstore.ParseVersion(built)
	l, err := wmstore.ParseVersion(latest)
	if err == nil && b.LessThan(l) {
		return latest, "latest"
	}
	return built, "built-in"
}

func parseVersion(value string) (wmstore.WAVersionContainer, error) {
	v, err := wmstore.ParseVersion(value)
	if err != nil {
		return v, fmt.Errorf("parse WhatsApp version %q: %w", value, err)
	}
	return v, nil
}
