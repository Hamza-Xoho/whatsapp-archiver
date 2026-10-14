package media

import (
	"fmt"
	"slices"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func decide(m store.Media, r config.MediaRules) (string, string) {
	if slices.Contains(r.Skip, m.Type) {
		return "skipped", "type skipped"
	}
	if m.Type == "video" && r.VideoMaxMB > 0 && m.DeclaredSize > int64(r.VideoMaxMB)<<20 {
		return "skipped", fmt.Sprintf("over %d MB cap", r.VideoMaxMB)
	}
	if !slices.Contains(r.Download, m.Type) {
		return "skipped", "type not enabled"
	}
	return "pending", ""
}
