// Package audit records security- and safety-relevant API actions: robot
// commands, firmware builds and flashes, WebSocket tickets and rejected
// authentication. Each record is one structured log line (JSON when written
// to a file) with who, what, outcome and duration.
package audit

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bzyfuzy/ratavyr/internal/auth"
)

type notesKey struct{}

type notes struct{ attrs []any }

// Note attaches key/value details (e.g. command parameters) to the audit
// record of the current request. It is a no-op outside an audited request.
func Note(ctx context.Context, kv ...any) {
	if n, ok := ctx.Value(notesKey{}).(*notes); ok {
		n.attrs = append(n.attrs, kv...)
	}
}

// Logger writes audit records. A nil *Logger discards them.
type Logger struct {
	l *slog.Logger
}

// New returns an audit logger writing to l.
func New(l *slog.Logger) *Logger {
	if l == nil {
		return nil
	}
	return &Logger{l: l.With("audit", true)}
}

// Record writes one entry.
func (a *Logger) Record(ctx context.Context, r *http.Request, action string, attrs ...any) {
	if a == nil {
		return
	}
	base := []any{
		"action", action,
		"role", auth.RoleFrom(r.Context()).String(),
		"remote", auth.ClientIP(r),
		"method", r.Method,
		"path", r.URL.Path,
	}
	a.l.Log(ctx, slog.LevelInfo, "audit", append(base, attrs...)...)
}

// statusWriter captures the response status and error code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer
// (needed by the WebSocket upgrade on wrapped writers).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// audited reports whether a request is recorded.
func audited(r *http.Request) (string, bool) {
	p := r.URL.Path
	switch {
	case r.Method != http.MethodPost:
		return "", false
	case strings.HasPrefix(p, "/api/commands/"):
		return "command." + strings.TrimPrefix(p, "/api/commands/"), true
	case p == "/api/firmware/builds":
		return "firmware.build", true
	case p == "/api/firmware/flashes":
		return "firmware.flash", true
	case p == "/api/ws-tickets":
		return "ws.ticket", true
	}
	return "", false
}

// Middleware records every audited request with its outcome. It must run
// inside the auth middleware so the role is known.
func (a *Logger) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action, ok := audited(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		n := &notes{}
		r = r.WithContext(context.WithValue(r.Context(), notesKey{}, n))
		next.ServeHTTP(sw, r)
		outcome := "ok"
		if sw.status >= 400 {
			outcome = "rejected"
		}
		attrs := append([]any{
			"status", sw.status,
			"outcome", outcome,
			"duration_ms", time.Since(start).Milliseconds(),
		}, n.attrs...)
		a.Record(r.Context(), r, action, attrs...)
	})
}
