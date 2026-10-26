package store

import (
	"strings"
	"unicode"
)

// Fold is the FR-10 folder, applied to the indexed text and to the query so that spelling variants meet (spike 8):
// the Arabic diacritics (harakat U+064B to U+065F and the dagger alef U+0670) and the tatweel are dropped, the alef
// forms with hamza or madda become a plain alef, ta marbuta becomes ha, alef maqsura becomes ya, and everything is
// lower-cased. It does no Unicode normalisation (no NFKC) and keeps digits, punctuation and emoji as they are.
func Fold(s string) string { return strings.Map(foldRune, s) }

func foldRune(r rune) rune {
	switch {
	case r >= 0x064B && r <= 0x065F, r == 0x0670, r == 0x0640:
		return -1
	case r == 'أ', r == 'إ', r == 'آ':
		return 'ا'
	case r == 'ة':
		return 'ه'
	case r == 'ى':
		return 'ي'
	}
	return unicode.ToLower(r)
}
