package web

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// requestWriter records response facts while preserving optional HTTP interfaces.
type requestWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *requestWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *requestWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *requestWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *requestWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

func (w *requestWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func timeRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rw := &requestWriter{ResponseWriter: w}
			defer func() {
				status := rw.status
				if status == 0 {
					status = http.StatusOK
				}
				log.Info("http request", "method", r.Method, "route", routePattern(r), "status", status, "bytes", rw.bytes, "duration", time.Since(started))
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

func recoverRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() == nil {
					return
				}
				log.Error("request panic", "method", r.Method, "route", routePattern(r))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func routePattern(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}
	return r.Pattern
}
