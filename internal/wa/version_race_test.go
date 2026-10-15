package wa

import (
	"sync"
	"testing"

	"whatsapp-archiver/internal/config"
)

// The health monitor reads the version from its own goroutines while the reconnect loop refreshes it after a
// ClientOutdated; the pair must be safe to read concurrently (run with -race).
func TestVersionAccessorsAreSafeDuringRefresh(t *testing.T) {
	c := &Client{cfg: &config.Config{WA: config.WAConfig{VersionOverride: "2.3000.7"}}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			if _, err := c.refreshVersion(t.Context()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_, _ = c.VersionInUse(), c.VersionReason()
		}
	}()
	wg.Wait()
	if c.VersionInUse() != "2.3000.7" || c.VersionReason() != "override" {
		t.Fatalf("version=%q reason=%q", c.VersionInUse(), c.VersionReason())
	}
}
