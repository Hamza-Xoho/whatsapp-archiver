package store_test

import (
	"testing"

	"whatsapp-archiver/internal/store"
)

// FR-10: the folder both the indexed text and the query go through (spike 8).
func TestFold(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain ascii is lower-cased", "Hello WORLD", "hello world"},
		{"harakat are stripped", "كِتَاب", "كتاب"},
		{"fathatan to sukun and shadda", "كتابٌ مُحَمَّد ً ٍ ْ", "كتاب محمد   "},
		{"dagger alef is stripped", "هٰذا", "هذا"},
		{"last stripped mark U+065F", "بٟب", "بب"},
		{"tatweel is stripped", "مـدرسـة", "مدرسه"},
		{"alef with hamza above", "أحمد", "احمد"},
		{"alef with hamza below", "إسلام", "اسلام"},
		{"alef with madda", "آمن", "امن"},
		{"ta marbuta to ha", "رحمة", "رحمه"},
		{"alef maqsura to ya", "إلى على", "الي علي"},
		{"mixed script", "Hello صَدِيقِي OK", "hello صديقي ok"},
		{"digits and arabic-indic digits are kept", "٠١٢ 0501234567", "٠١٢ 0501234567"},
		{"emoji and punctuation are kept", "😀 hi!", "😀 hi!"},
		{"url", "HTTPS://Example.com/Path", "https://example.com/path"},
		{"already folded", "كتاب", "كتاب"},
		{"only marks", "ًـّ", ""},
		{"accents on latin letters are kept", "Café", "café"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := store.Fold(c.in); got != c.want {
				t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := store.Fold(store.Fold(c.in)); again != store.Fold(c.in) {
				t.Errorf("Fold is not idempotent on %q: %q then %q", c.in, store.Fold(c.in), again)
			}
		})
	}
}
