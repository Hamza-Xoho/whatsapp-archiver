package web

import (
	"os"
	"sort"

	"whatsapp-archiver/internal/view"
)

func statSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// sizeOfDir is the folder's size in words, or "unavailable" when it cannot be read.
func sizeOfDir(dir string) string {
	n, err := view.DirSize(dir)
	if err != nil {
		return "unavailable"
	}
	return view.HumanSize(n)
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
