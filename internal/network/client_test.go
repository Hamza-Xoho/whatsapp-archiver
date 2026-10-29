package network

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
)

func TestDialFallsBackFromSystemDNSAndPersistsLastKnownGood(t *testing.T) {
	settings := &settingsFake{}
	cache, err := loadIPCache(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	var lookups []string
	system := func(context.Context, string) ([]net.IPAddr, error) {
		lookups = append(lookups, "system")
		return nil, errors.New("system DNS down")
	}
	fallback := func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookups = append(lookups, "fallback:"+host)
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil
	}
	var dialed string
	dialIP := func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		left, right := net.Pipe()
		t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
		return left, nil
	}

	d := newFallbackDialer([]lookupFunc{system, fallback}, cache, dialIP)
	conn, err := d.DialContext(t.Context(), "tcp", "web.whatsapp.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if !slices.Equal(lookups, []string{"system", "fallback:web.whatsapp.com"}) {
		t.Fatalf("lookups=%v", lookups)
	}
	if dialed != "203.0.113.9:443" {
		t.Fatalf("dialed=%q", dialed)
	}
	if got := cache.IP("web.whatsapp.com"); got != "203.0.113.9" {
		t.Fatalf("cached IP=%q", got)
	}
	if settings.writes != 1 || settings.value == "" {
		t.Fatalf("settings writes=%d value=%q", settings.writes, settings.value)
	}
}

func TestDialUsesLastKnownGoodAfterEveryResolverFails(t *testing.T) {
	settings := &settingsFake{value: `{"web.whatsapp.com":"198.51.100.7"}`, present: true}
	cache, err := loadIPCache(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	fail := func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("DNS down") }
	var dialed string
	d := newFallbackDialer([]lookupFunc{fail, fail}, cache, func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		left, right := net.Pipe()
		t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
		return left, nil
	})
	conn, err := d.DialContext(t.Context(), "tcp", "web.whatsapp.com:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if dialed != "198.51.100.7:443" {
		t.Fatalf("dialed=%q", dialed)
	}
}

type settingsFake struct {
	value   string
	present bool
	writes  int
}

func (s *settingsFake) Setting(context.Context, string) (string, bool, error) {
	return s.value, s.present, nil
}

func (s *settingsFake) SetSetting(_ context.Context, _ string, value string) error {
	s.value, s.present = value, true
	s.writes++
	return nil
}
