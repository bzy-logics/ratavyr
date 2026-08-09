// Package api assembles the MotionCore REST/WebSocket API (docs/rest-api.md):
// routes, authentication and optional static web UI serving. Handler logic
// lives in the robots, plugins, firmware and websocket packages; JSON types,
// proto conversion and error mapping live in api/rest.
package api

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/bzyfuzy/ratavyr/internal/api/rest"
	"github.com/bzyfuzy/ratavyr/internal/auth"
	"github.com/bzyfuzy/ratavyr/internal/engine"
	"github.com/bzyfuzy/ratavyr/internal/firmware"
	"github.com/bzyfuzy/ratavyr/internal/plugins"
	"github.com/bzyfuzy/ratavyr/internal/robots"
	"github.com/bzyfuzy/ratavyr/internal/websocket"
)

// ServerVersion is reported by GET /api/info. Overridable at link time with
// -ldflags "-X github.com/bzyfuzy/ratavyr/internal/api.ServerVersion=...".
var ServerVersion = "0.2.0"

// Options configures the handler.
type Options struct {
	Engine *engine.Client
	Log    *slog.Logger
	// Token, when non-empty, is required on /api/* (see package auth).
	Token string
	// WebDir, if an existing directory, is served at / with SPA fallback.
	WebDir string
	// OriginPatterns are extra allowed WebSocket origins.
	OriginPatterns []string
}

// New returns the HTTP handler.
func New(o Options) http.Handler {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteJSON(w, http.StatusOK, Health{Status: "ok", Engine: EngineStatus{Connected: o.Engine.Connected()}})
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteJSON(w, http.StatusOK, Info{
			ServerVersion: ServerVersion,
			AuthRequired:  o.Token != "",
			Engine:        rest.FromEngineInfo(o.Engine.Info()),
		})
	})
	(&robots.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&plugins.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&firmware.Handler{Engine: o.Engine, Log: log}).Register(mux)
	(&websocket.Handler{Engine: o.Engine, Log: log, OriginPatterns: o.OriginPatterns}).Register(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	if o.WebDir != "" {
		if st, err := os.Stat(o.WebDir); err == nil && st.IsDir() {
			mux.Handle("/", spa(o.WebDir))
			log.Info("serving web UI", "dir", o.WebDir)
		} else {
			log.Warn("web UI directory not found, not serving static files", "dir", o.WebDir)
		}
	}
	return auth.Middleware(o.Token, mux)
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
