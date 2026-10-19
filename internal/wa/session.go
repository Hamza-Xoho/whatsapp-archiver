package wa

import "path/filepath"

func sessionDSN(path string) string {
	// _txlock=immediate: whatsmeow's read-then-write decrypt transactions take the write lock at BEGIN,
	// so they wait on busy_timeout instead of failing with SQLITE_BUSY mid-transaction.
	return "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
}
