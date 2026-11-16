// Package websocket implements GET /api/ws, which pushes engine connection
// status, telemetry and log events to clients. Commands go through REST, so
// incoming messages are discarded.
package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/bzy-logics/ratavyr/internal/api/rest"
	"github.com/bzy-logics/ratavyr/internal/engine"
	"github.com/coder/websocket"
)

const writeWait = 5 * time.Second

// Handler streams engine events to WebSocket clients.
type Handler struct {
	Engine *engine.Client
	Log    *slog.Logger
	// OriginPatterns allows cross-origin connections from these host
	// patterns (e.g. "localhost:5173" for the Vite dev server). Same-origin is
	// always allowed.
	OriginPatterns []string
}

// Message is a server -> client WebSocket message.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// EngineStatus is the data of an "engine" message.
type EngineStatus struct {
	Connected bool `json:"connected"`
}

// Register adds the route to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/ws", h)
}

// ServeHTTP upgrades the connection and streams events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := h.Log
	if log == nil {
		log = slog.Default()
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.OriginPatterns})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())

	events, cancel := h.Engine.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				log.Warn("closing slow websocket client", "remote", r.RemoteAddr)
				c.Close(websocket.StatusTryAgainLater, "client too slow")
				return
			}
			msg, err := json.Marshal(Convert(ev))
			if err != nil {
				continue
			}
			wctx, wcancel := context.WithTimeout(ctx, writeWait)
			err = c.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

// Convert shapes an engine event as a WebSocket message.
func Convert(ev engine.Event) Message {
	switch ev.Type {
	case engine.EventTelemetry:
		return Message{Type: "telemetry", Data: rest.FromTelemetry(ev.Telemetry)}
	case engine.EventLog:
		return Message{Type: "log", Data: rest.FromLogEntry(ev.Log)}
	default:
		return Message{Type: "engine", Data: EngineStatus{Connected: ev.Connected}}
	}
}
