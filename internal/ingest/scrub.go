package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
)

var jidPattern = regexp.MustCompile(`([0-9]{4,})@(s\.whatsapp\.net|lid)`)

func Scrub(in, out string) error {
	mapping := map[string]string{}
	return filepath.WalkDir(in, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(in, path)
		target := filepath.Join(out, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		data = jidPattern.ReplaceAllFunc(data, func(value []byte) []byte {
			key := string(value)
			if fake, ok := mapping[key]; ok {
				return []byte(fake)
			}
			sum := sha256.Sum256(value)
			suffix := "@lid"
			if jidPattern.FindSubmatch(value)[2][0] == 's' {
				suffix = "@s.whatsapp.net"
			}
			fake := hex.EncodeToString(sum[:6]) + suffix
			mapping[key] = fake
			return []byte(fake)
		})
		return os.WriteFile(target, data, 0o600)
	})
}
