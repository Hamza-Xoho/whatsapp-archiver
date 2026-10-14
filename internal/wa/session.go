package wa

import "path/filepath"

func sessionDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}
