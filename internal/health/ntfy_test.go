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

func TestNtfyPostsToTopicPath(t *testing.T) {
	var path string
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { path = r.URL.Path }))
	defer s.Close()
	if err := NewNtfy(&config.NtfyConfig{URL: s.URL + "/archiver-alerts"}).Notify(context.Background(), "t", "b"); err != nil {
		t.Fatal(err)
	}
	if path != "/archiver-alerts" {
		t.Fatalf("path=%q", path)
	}
}

func TestNtfyUsesProvidedHTTPClient(t *testing.T) {
	used := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		used = true
		return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: http.NoBody}, nil
	})}
	if err := NewNtfyWithClient(&config.NtfyConfig{URL: "https://ntfy.invalid/topic"}, client).Notify(t.Context(), "t", "b"); err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("provided HTTP client was not used")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
