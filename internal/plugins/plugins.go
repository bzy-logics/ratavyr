// Package plugins implements GET /api/plugins.
package plugins

import (
	"log/slog"
	"net/http"

	enginev1 "github.com/bzyfuzy/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzyfuzy/ratavyr/internal/api/rest"
	"github.com/bzyfuzy/ratavyr/internal/engine"
)

// Handler serves the plugin list relayed from the engine.
type Handler struct {
	Engine *engine.Client
	Log    *slog.Logger
}

// Register adds the routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/plugins", h.list)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	resp, err := engine.Call(r.Context(), h.Engine, h.Engine.Engine.ListPlugins, &enginev1.ListPluginsRequest{})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "ListPlugins", err)
		return
	}
	rest.WriteJSON(w, http.StatusOK, rest.FromPlugins(resp.GetPlugins()))
}
