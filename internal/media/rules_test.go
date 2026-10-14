package media

import (
	"testing"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestDecideSkipsStickerAndLargeVideo(t *testing.T) {
	r := config.MediaRules{Download: []string{"video"}, Skip: []string{"sticker", "gif"}, VideoMaxMB: 100}
	if a, _ := decide(store.Media{Type: "sticker"}, r); a != "skipped" {
		t.Fatal(a)
	}
	if a, reason := decide(store.Media{Type: "video", DeclaredSize: 150 << 20}, r); a != "skipped" || reason != "over 100 MB cap" {
		t.Fatalf("%s %s", a, reason)
	}
}
func TestDecideDownloadsDocument(t *testing.T) {
	r := config.MediaRules{Download: []string{"document"}}
	if a, _ := decide(store.Media{Type: "document"}, r); a != "pending" {
		t.Fatal(a)
	}
}
