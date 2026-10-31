package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type exportBodyGateWriter struct {
	http.ResponseWriter
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *exportBodyGateWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return w.ResponseWriter.Write(p)
}

func (w *exportBodyGateWriter) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
}

func TestExportHeadersReachClientBeforeBodyPreparationRelease(t *testing.T) {
	u, chat := exportUI(t)
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.handler().ServeHTTP(&exportBodyGateWriter{ResponseWriter: w, started: started, release: release}, r)
	}))
	defer server.Close()
	client := server.Client()
	defer client.CloseIdleConnections()

	result := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	var releaseOnce sync.Once
	releaseBody := func() { releaseOnce.Do(func() { close(release) }) }
	go func() {
		response, err := client.Get(server.URL + "/chats/" + chat + "/export/download?format=json")
		if err != nil {
			errs <- err
			return
		}
		result <- response
	}()
	defer releaseBody()
	select {
	case <-started:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("export body preparation did not reach its release gate")
	}

	var response *http.Response
	select {
	case response := <-result:
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Disposition") == "" {
			t.Fatalf("export headers = status %d disposition %q", response.StatusCode, response.Header.Get("Content-Disposition"))
		}
		releaseBody()
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(250 * time.Millisecond):
		releaseBody()
		select {
		case response = <-result:
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		case err := <-errs:
			t.Fatal(err)
		case <-time.After(2 * time.Second):
			t.Fatal("export request did not finish after body release")
		}
		t.Fatal("Client.Do did not receive export headers before body preparation release")
	}
}
