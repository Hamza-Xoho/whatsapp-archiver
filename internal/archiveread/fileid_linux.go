//go:build linux

package archiveread

import (
	"os"
	"syscall"
)

func identityFields(info os.FileInfo) any {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return [2]uint64{uint64(stat.Dev), stat.Ino}
	}
	return info.Name()
}
