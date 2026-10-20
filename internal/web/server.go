// Package web is the control UI: server-rendered html/template pages with htmx, and one Server-Sent Events stream fed
// by the activity bus. It runs inside `archiver run`, listens on loopback only (no login in phase 2; reached with
// ssh -L), and never imports the WhatsApp client: pairing reaches it as plain strings through Pairer.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"time"

	"whatsapp-archiver/internal/activity"
	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/health"
	"whatsapp-archiver/internal/ingest"
	"whatsapp-archiver/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

// static holds the vendored htmx 2.0.4 and htmx-ext-sse 2.2.2 (pinned by SHA-256 in assets_test.go) and app.css.
//
//go:embed static
var staticFiles embed.FS

// shutdownBound is how long Run waits for requests in flight once its context ends.
const shutdownBound = 5 * time.Second

// Deps is what the UI reads and writes. Writes go through Store's write methods only (one writer).
type Deps struct {
	Store    *store.Store
	Rules    *ingest.RuleSet
	Health   func() health.Snapshot
	Activity *activity.Bus
	Config   *config.Config
	Pairer   Pairer
	Now      func() time.Time
}

// Pairer pairs WhatsApp from the browser (implemented in internal/wa in Task 10). Updates are plain strings: the QR
// code text, then Done, or Err.
type Pairer interface {
	Paired() bool
	StartPairing(ctx context.Context) (<-chan PairUpdate, error)
}

// PairUpdate is one step of pairing: a QR code to show, the end of pairing, or why it failed.
type PairUpdate struct {
	QR   string
	Done bool
	Err  string
}

// Server is the control UI's HTTP server.
type Server struct {
	d           Deps
	tmpl        *template.Template // parsed once from templateFiles
	static      http.Handler
	healthEvery time.Duration // how often a quiet stream repeats the health event
}

// New parses the embedded templates once and builds the server.
func New(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the embed directive guarantees the directory
	}
	return &Server{
		d:           d,
		tmpl:        template.Must(template.ParseFS(templateFiles, "templates/*.html")),
		static:      http.StripPrefix("/static/", fileOnly(http.FileServerFS(sub), sub)),
		healthEvery: 15 * time.Second,
	}
}

// Handler is every route behind the host and origin guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return guard(mux)
}

// routes registers the pages. Later tasks add theirs here.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.dashboard)
	mux.HandleFunc("GET /events", s.events)
	mux.Handle("GET /static/", s.static)
}

// page is what the layout renders.
type page struct {
	Title string
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "layout", page{Title: "Dashboard"})
}

// render executes a template into memory first, so a failure is a clean 500 and never half a page.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// fileOnly answers 404 unless the path names an embedded file, so /static/ never lists the folder: its stripped path
// is empty, which fs.Stat rejects, and the folder has no subfolders.
func fileOnly(next http.Handler, root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(root, r.URL.Path); err != nil {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Run serves the UI on listen until ctx ends, then shuts down within shutdownBound. Request contexts derive from ctx,
// so open SSE streams end at once instead of holding the shutdown. A listen failure is returned at once.
func (s *Server) Run(ctx context.Context, listen string) error {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("web: listen on %s: %w", listen, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }() // ends when Shutdown or Close below runs
	select {
	case err := <-served:
		return fmt.Errorf("web: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownBound)
	defer cancel()
	err = srv.Shutdown(sctx)
	if err != nil {
		err = errors.Join(fmt.Errorf("web: shutdown: %w", err), srv.Close())
	}
	if serr := <-served; !errors.Is(serr, http.ErrServerClosed) {
		err = errors.Join(err, serr)
	}
	return err
}
