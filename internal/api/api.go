// Package api assembles the MotionCore REST/WebSocket API (docs/rest-api.md):
// routes, request guards, authentication, authorization, auditing and
// optional static web UI serving. Handler logic lives in the robots, plugins,
// firmware and websocket packages; JSON types, proto conversion and error
// mapping live in api/rest.
//
// Request pipeline (outermost first):
//
//	guard (Host / Origin / Content-Type checks, POST rate limit)
//	  -> auth (token or WebSocket ticket -> role)
//	    -> audit (commands, firmware, tickets)
//	      -> routes (per-route role checks)
package api

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bzyfuzy/ratavyr/internal/api/rest"
	"github.com/bzyfuzy/ratavyr/internal/audit"
	"github.com/bzyfuzy/ratavyr/internal/auth"
	"github.com/bzyfuzy/ratavyr/internal/engine"
	"github.com/bzyfuzy/ratavyr/internal/firmware"
	"github.com/bzyfuzy/ratavyr/internal/guard"
	"github.com/bzyfuzy/ratavyr/internal/plugins"
	"github.com/bzyfuzy/ratavyr/internal/robots"
	"github.com/bzyfuzy/ratavyr/internal/websocket"
)

// ServerVersion is reported by GET /api/info. Overridable at link time with
// -ldflags "-X github.com/bzyfuzy/ratavyr/internal/api.ServerVersion=...".
var ServerVersion = "0.3.0"

// Options configures the handler.
type Options struct {
	Engine *engine.Client
	Log    *slog.Logger
	// Audit receives audit records; nil writes them to Log.
	Audit *audit.Logger
	// Token is the admin token (kept for compatibility; same as Tokens.Admin).
	Token string
	// Tokens are the per-role tokens. Authentication is off when none is set.
	Tokens auth.Tokens
	// WebDir, if an existing directory, is served at / with SPA fallback.
	WebDir string
	// OriginPatterns are extra allowed cross-origin hosts.
	OriginPatterns []string
	// AllowedHosts are extra Host values accepted while auth is off.
	AllowedHosts []string
	// RateLimit / RateBurst limit POST requests per client IP.
	RateLimit, RateBurst float64
}

// New returns the HTTP handler.
func New(o Options) http.Handler {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	tokens := o.Tokens
	if tokens.Admin == "" {
		tokens.Admin = o.Token
	}
	aud := o.Audit
	if aud == nil {
		aud = audit.New(log)
	}
	authn := auth.New(tokens)
	authn.OnFailure = func(r *http.Request, reason string) {
		aud.Record(r.Context(), r, "auth.rejected", "reason", reason)
	}
	authn.OnDeprecatedQueryToken = func(r *http.Request) {
		log.Warn("WebSocket authenticated with ?access_token= (deprecated: use POST /api/ws-tickets)", "remote", auth.ClientIP(r))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteJSON(w, http.StatusOK, Health{Status: "ok", Engine: EngineStatus{Connected: o.Engine.Connected()}})
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		info := Info{
			ServerVersion: ServerVersion,
			AuthRequired:  authn.Enabled(),
			Engine:        rest.FromEngineInfo(o.Engine.Info()),
		}
		if err := o.Engine.LastError(); err != nil {
			msg := err.Error()
			info.EngineError = &msg
		}
		rest.WriteJSON(w, http.StatusOK, info)
	})
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteJSON(w, http.StatusOK, SessionFor(authn.Enabled(), auth.RoleFrom(r.Context())))
	})
	mux.HandleFunc("POST /api/ws-tickets", auth.Require(auth.Viewer, func(w http.ResponseWriter, r *http.Request) {
		if !rest.DecodeObject(w, r, &struct{}{}) {
			return
		}
		t, ttl, err := authn.IssueTicket(auth.RoleFrom(r.Context()))
		if err != nil {
			rest.WriteError(w, http.StatusInternalServerError, "internal", "cannot create ticket")
			return
		}
		rest.WriteJSON(w, http.StatusCreated, Ticket{Ticket: t, ExpiresInMS: ttl.Milliseconds()})
	}))
	(&robots.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&plugins.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&firmware.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&websocket.Handler{Engine: o.Engine, Log: log, OriginPatterns: o.OriginPatterns}).Register(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if allow := allowedMethods(mux, r); len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			rest.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" is not supported here")
			return
		}
		rest.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	if o.WebDir != "" {
		if st, err := os.Stat(o.WebDir); err == nil && st.IsDir() {
			mux.Handle("/", securityHeaders(spa(o.WebDir)))
			log.Info("serving web UI", "dir", o.WebDir)
		} else {
			log.Warn("web UI directory not found, not serving static files", "dir", o.WebDir)
		}
	}
	g := guard.New(guard.Options{
		AuthEnabled:    authn.Enabled(),
		OriginPatterns: o.OriginPatterns,
		AllowedHosts:   o.AllowedHosts,
		Rate:           o.RateLimit,
		Burst:          o.RateBurst,
	})
	return g.Middleware(authn.Middleware(aud.Middleware(apiHeaders(mux))))
}

// allowedMethods lists the methods that do have a route for r's path.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var out []string
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/api/" {
			out = append(out, m)
		}
	}
	return out
}

// apiHeaders marks API responses as uncacheable and not sniffable.
func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders hardens the served web UI.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// Health is the GET /api/health response.
type Health struct {
	Status string       `json:"status"`
	Engine EngineStatus `json:"engine"`
}

// EngineStatus is the engine connection status.
type EngineStatus struct {
	Connected bool `json:"connected"`
}

// Info is the GET /api/info response.
type Info struct {
	ServerVersion string           `json:"server_version"`
	AuthRequired  bool             `json:"auth_required"`
	Engine        *rest.EngineInfo `json:"engine"`
	// EngineError says why the engine is not connected (e.g. an
	// incompatible api_version); null while connected.
	EngineError *string `json:"engine_error"`
}

// Session is the GET /api/session response.
type Session struct {
	AuthRequired bool        `json:"auth_required"`
	Role         string      `json:"role"`
	Permissions  Permissions `json:"permissions"`
}

// Permissions are what the current role may do. The server enforces them;
// clients use them to hide or disable controls.
type Permissions struct {
	View          bool `json:"view"`
	Stop          bool `json:"stop"`
	Command       bool `json:"command"`
	FirmwareBuild bool `json:"firmware_build"`
	FirmwareFlash bool `json:"firmware_flash"`
}

// SessionFor describes role.
func SessionFor(authRequired bool, role auth.Role) Session {
	return Session{
		AuthRequired: authRequired,
		Role:         role.String(),
		Permissions: Permissions{
			View:          role >= auth.Viewer,
			Stop:          role >= robots.RoleFor("estop"),
			Command:       role >= robots.RoleFor("jog"),
			FirmwareBuild: role >= firmware.BuildRole,
			FirmwareFlash: role >= firmware.FlashRole,
		},
	}
}

// Ticket is the POST /api/ws-tickets response.
type Ticket struct {
	Ticket      string `json:"ticket"`
	ExpiresInMS int64  `json:"expires_in_ms"`
}

// spa serves files from dir, falling back to index.html for unknown paths.
func spa(dir string) http.Handler {
	root := http.Dir(dir)
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := root.Open(path.Clean("/" + r.URL.Path)); err == nil {
			st, err := f.Stat()
			f.Close()
			if err == nil && (!st.IsDir() || r.URL.Path == "/") {
				files.ServeHTTP(w, r)
				return
			}
		}
		f, err := os.Open(filepath.Join(dir, "index.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "index.html", st.ModTime(), f)
	})
}
