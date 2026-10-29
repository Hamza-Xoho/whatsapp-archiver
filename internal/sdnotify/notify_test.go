//go:build !windows

package sdnotify

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifySendsUnixgramDatagram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.sock")
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", path)

	if err := Notify("READY=1"); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, _, err := listener.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Fatalf("datagram=%q", got)
	}
}
