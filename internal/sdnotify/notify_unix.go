//go:build unix

package sdnotify

import (
	"net"
	"os"
	"strings"
)

// Notify sends one datagram to systemd's NOTIFY_SOCKET. With no socket (normal
// tests and non-systemd runs), it is a no-op.
func Notify(message string) error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "@") { // systemd's abstract Linux socket notation
		path = "\x00" + path[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte(message))
	return err
}
