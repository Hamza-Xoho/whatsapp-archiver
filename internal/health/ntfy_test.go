package health

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"whatsapp-archiver/internal/config"
)

func TestNtfyPostsTokenTitleAndBody(t *testing.T) {
	var got string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.Header.Get("Title") + " " + r.Header.Get("Authorization") + " " + string(b)
	}))
	defer s.Close()
	n := NewNtfy(&config.NtfyConfig{URL: s.URL + "/topic", Token: "secret"})
	if err := n.Notify(context.Background(), "Alert", "body"); err != nil {
		t.Fatal(err)
	}
	if got != "POST Alert Bearer secret body" {
		t.Fatal(got)
	}
}
