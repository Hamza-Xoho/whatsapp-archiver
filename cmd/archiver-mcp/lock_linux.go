//go:build linux

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errLocked = errors.New("BUSY")

// The persistent lock inode is never unlinked: unlinking would allow another
// process to lock a different inode while the first process still owns its lock.
func acquireLock(runtimeDir string) (*os.File, error) {
	dirfd, err := unix.Open(runtimeDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(dirfd) }()
	fd, err := unix.Openat(dirfd, "archiver-mcp.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "archiver-mcp.lock")
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		_ = file.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || int(st.Uid) != os.Geteuid() {
		_ = file.Close()
		return nil, errors.New("unsafe lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errLocked
		}
		return nil, err
	}
	return file, nil
}
