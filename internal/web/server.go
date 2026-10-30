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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
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

// templateFuncs are the helpers every page may call. pathEscape makes one path segment of a value that came from
// another phone (a message id may hold "?" or "/").
var templateFuncs = template.FuncMap{"pathEscape": url.PathEscape}

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
	// Notifier sends the test alert; nil when ntfy is not configured. It is the monitor's own notifier.
	Notifier health.Notifier
	// DiskFree reports the free bytes of the disk holding a path; nil: health.DiskFree.
	DiskFree func(string) (uint64, error)
	// Logger receives request timing and panic records. Nil uses slog.Default.
	Logger *slog.Logger
	// Listening is called with the address once Run has the listener, before it serves; nil: nothing.
	Listening func(addr string)
}

// Pairer pairs WhatsApp from the browser (wa.Pairer, through an adapter in cmd/archiver). Updates are plain strings:
// the QR code text, then Done, or Err; the Pairer closes the channel when pairing ends or the archiver stops.
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
	tmpl        *template.Template            // the layout and shared fragments, parsed once from templateFiles
	pages       map[string]*template.Template // one clone of tmpl per page, each with its own "content" block
	static      http.Handler
	healthEvery time.Duration // how often a quiet stream repeats the health event
	// rulesMu serializes rule changes from read to Replace, so two interleaved POSTs cannot leave the live rules
	// without one of them.
	rulesMu sync.Mutex
	// storageCache keeps the slow storage figures for five minutes.
	storageCache storageCache
	// testAlertAt throttles the test alert button.
	testAlertMu sync.Mutex
	testAlertAt time.Time
	// pair is the browser pairing's state.
	pair *pairing
}

// New parses the embedded templates once and builds the server.
func New(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.DiskFree == nil {
		d.DiskFree = health.DiskFree
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the embed directive guarantees the directory
	}
	s := &Server{d: d}
	// notPaired shows the layout's banner; pages are rendered after New returns, so s is complete by then.
	funcs := template.FuncMap{"notPaired": func() bool { return s.d.Pairer != nil && !s.d.Pairer.Paired() }}
	base := template.Must(template.New("layout.html").Funcs(templateFuncs).Funcs(funcs).ParseFS(templateFiles, "templates/layout.html"))
	*s = Server{
		d:    d,
		tmpl: base,
		pages: map[string]*template.Template{
			"dashboard": pageTemplate(base, "dashboard"),
			"logging":   pageTemplate(base, "logging"),
			"chats":     pageTemplate(base, "chats"),
			"viewer":    pageTemplate(base, "viewer"),
			"trace":     pageTemplate(base, "trace"),
			"settings":  pageTemplate(base, "settings"),
			"storage":   pageTemplate(base, "storage"),
			"backups":   pageTemplate(base, "backups"),
			"pair":      pageTemplate(base, "pair"),
		},
		pair:        newPairing(),
		static:      http.StripPrefix("/static/", fileOnly(http.FileServerFS(sub), sub)),
		healthEvery: 15 * time.Second,
	}
	return s
}

// pageTemplate is the layout with one page's templates/<name>.html on top. Pages cannot share a template set: each
// defines its own "content" block, and the last one parsed would win. Every page is therefore a clone of the layout
// parsed with its own file. Add a page by creating templates/<name>.html and listing it in New.
func pageTemplate(layout *template.Template, name string) *template.Template {
	return template.Must(template.Must(layout.Clone()).ParseFS(templateFiles, "templates/"+name+".html"))
}

// Handler is every route behind the host and origin guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return guard(recoverRequests(s.d.Logger)(timeRequests(s.d.Logger)(mux)))
}

// routes registers the pages. Later tasks add theirs here.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chats", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /logging", s.logging)
	mux.HandleFunc("GET /partials/health", s.healthPartial)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /chats", s.chats)
	mux.HandleFunc("POST /chats/rules", s.chatRules)
	mux.HandleFunc("POST /chats/ack", s.chatAck)
	mux.HandleFunc("GET /chats/{jid}", s.viewer)
	mux.HandleFunc("GET /chats/{jid}/m/{id}", s.trace)
	mux.HandleFunc("GET /media", s.media)
	mux.HandleFunc("GET /settings", s.settings)
	mux.HandleFunc("POST /settings/{section}", s.saveSetting)
	mux.HandleFunc("GET /storage", s.storage)
	mux.HandleFunc("GET /backups", s.backups)
	mux.HandleFunc("POST /backups/test-alert", s.testAlert)
	mux.HandleFunc("GET /pair", s.pairPage)
	mux.HandleFunc("POST /pair/start", s.pairStart)
	mux.HandleFunc("GET /pair/qr.png", s.pairQR)
	mux.Handle("GET /static/", s.static)
}

// page is what the layout renders; a page's own data embeds it.
type page struct {
	Title, Current string
	Live           bool
}

// render executes the template name of t into memory first, so a failure is a clean 500 and never half a page.
func (s *Server) render(w http.ResponseWriter, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
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
	if s.d.Listening != nil {
		s.d.Listening(l.Addr().String())
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
