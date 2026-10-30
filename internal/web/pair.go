package web

import (
	"bytes"
	"context"
	"net/http"
	"sync"

	"rsc.io/qr"

	"whatsapp-archiver/internal/view"
)

// Browser pairing (FR-1). POST /pair/start asks the Pairer to pair; one reader goroutine per pairing keeps the
// newest state here, GET /pair and GET /pair/qr.png render it, and each change reaches open pages as the SSE event
// "pair" (the pair panel's HTML). The QR code text stays on the server: pages and events only carry the image URL.

// pairing is the state of the browser pairing.
type pairing struct {
	mu      sync.Mutex
	ch      <-chan PairUpdate // the pairing being read; nil when none runs
	qr      string            // the current QR code; "" when none is shown
	seq     int               // counts QR codes, so the image URL changes with each one
	done    bool              // paired from this page since the archiver started
	err     string            // why the last pairing failed
	changed chan struct{}     // closed and replaced on every change
}

func newPairing() *pairing { return &pairing{changed: make(chan struct{})} }

// wait returns a channel closed at the next change.
func (p *pairing) wait() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

// notifyLocked wakes every stream waiting for a change; closing never blocks.
func (p *pairing) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// begin starts reading ch unless it is the pairing already being read (the Pairer returns the running pairing's
// channel to a second call).
func (s *Server) beginPairing(ch <-chan PairUpdate) {
	p := s.pair
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch == p.ch {
		return
	}
	p.ch, p.qr, p.err, p.done = ch, "", "", false
	p.notifyLocked()
	go s.readPairing(ch) // ends when the Pairer closes ch, which it does when pairing ends or the archiver stops
}

func (s *Server) readPairing(ch <-chan PairUpdate) {
	for u := range ch {
		s.pair.apply(ch, u)
	}
	p := s.pair
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != ch {
		return
	}
	p.ch, p.qr = nil, ""
	if !p.done && p.err == "" {
		p.err = "Pairing stopped before it finished."
	}
	p.notifyLocked()
}

func (p *pairing) apply(ch <-chan PairUpdate, u PairUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != ch {
		return
	}
	switch {
	case u.Done:
		p.done, p.qr = true, ""
	case u.Err != "":
		p.err, p.qr = u.Err, ""
	case u.QR != "":
		p.qr = u.QR
		p.seq++
	default:
		return
	}
	p.notifyLocked()
}

// pairActive reports whether a pairing runs or has succeeded here: the states a stream sends on connect.
func (s *Server) pairActive() bool {
	if s.d.Pairer == nil {
		return false
	}
	s.pair.mu.Lock()
	defer s.pair.mu.Unlock()
	return s.pair.ch != nil || s.pair.done
}

// pairView is what the pair panel shows.
type pairView struct {
	Available  bool // a Pairer is wired in
	Paired     bool // paired before this page paired it
	JustPaired bool // paired from this page
	Running    bool // a pairing is in progress
	HasQR      bool
	Seq        int
	Err        string
	History    []string
}

type pairData struct {
	page
	Pair pairView
}

func (s *Server) pairView(ctx context.Context) (pairView, error) {
	if s.d.Pairer == nil {
		return pairView{}, nil
	}
	p := s.pair
	p.mu.Lock()
	v := pairView{Available: true, JustPaired: p.done, Running: p.ch != nil && !p.done && p.err == "", HasQR: p.qr != "", Seq: p.seq, Err: p.err}
	p.mu.Unlock()
	if v.JustPaired || v.Running || !s.d.Pairer.Paired() {
		return v, nil
	}
	v.Paired, v.Err = true, ""
	if s.d.Store != nil {
		loc, err := view.Location(s.config())
		if err != nil {
			return v, err
		}
		cov, err := s.d.Store.HistoryCoverage(ctx)
		if err != nil {
			return v, err
		}
		v.History = view.HistoryLinesAt(loc, cov, s.d.Now())
	}
	return v, nil
}

// pairPage is GET /pair.
func (s *Server) pairPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.pairView(r.Context())
	if err != nil {
		http.Error(w, "the archive could not be read", http.StatusInternalServerError)
		return
	}
	s.render(w, s.pages["pair"], "layout", pairData{page: page{Title: "Pairing", Current: "Pairing", Live: v.Running || v.JustPaired}, Pair: v})
}

// pairStart is POST /pair/start: it asks the Pairer to pair (once; a pairing already running is kept) and sends the
// browser back to GET /pair.
func (s *Server) pairStart(w http.ResponseWriter, r *http.Request) {
	if s.d.Pairer == nil {
		http.NotFound(w, r)
		return
	}
	if !s.d.Pairer.Paired() {
		// Pairing outlives this request: the Pairer runs it under the archiver's own context.
		ch, err := s.d.Pairer.StartPairing(context.WithoutCancel(r.Context()))
		if err != nil {
			s.pair.mu.Lock()
			s.pair.err = "Pairing could not start: " + err.Error()
			s.pair.notifyLocked()
			s.pair.mu.Unlock()
		} else {
			s.beginPairing(ch)
		}
	}
	http.Redirect(w, r, "/pair", http.StatusSeeOther)
}

// pairQR is GET /pair/qr.png: the current QR code as a PNG (level L). It only renders; it never starts pairing.
func (s *Server) pairQR(w http.ResponseWriter, r *http.Request) {
	s.pair.mu.Lock()
	text := s.pair.qr
	s.pair.mu.Unlock()
	if text == "" {
		http.NotFound(w, r)
		return
	}
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		http.Error(w, "the QR code could not be drawn", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(code.PNG())
}

// writePair sends the pair panel as the SSE event "pair".
func (s *Server) writePair(ctx context.Context, w http.ResponseWriter) error {
	v, err := s.pairView(ctx)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := s.pages["pair"].ExecuteTemplate(&buf, "pair_panel", v); err != nil {
		return err
	}
	return writeEvent(w, "", "pair", buf.String())
}
