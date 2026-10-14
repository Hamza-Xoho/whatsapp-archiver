package main

import "testing"

func TestHumanSize(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0.0 B"}, {1023, "1023.0 B"}, {1024, "1.0 KB"}, {1536, "1.5 KB"}, {1<<20 - 1024, "1023.0 KB"}, {1 << 20, "1.0 MB"},
		{5<<20 + 1<<19, "5.5 MB"}, {1 << 30, "1.0 GB"}, {3 << 30, "3.0 GB"}, {1 << 40, "1024.0 GB"},
	} {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d)=%q want %q", c.n, got, c.want)
		}
	}
}
