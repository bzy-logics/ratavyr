// Package robots implements the robot status, configuration and command
// endpoints. All state is relayed from the engine; the server keeps no robot
// state machine of its own.
package robots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"

	enginev1 "github.com/bzyfuzy/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzyfuzy/ratavyr/internal/api/rest"
	"github.com/bzyfuzy/ratavyr/internal/engine"
)

// Handler serves /api/status, /api/config and /api/commands/{name}.
type Handler struct {
	Engine *engine.Client
	Log    *slog.Logger
}

// Register adds the routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", h.status)
	mux.HandleFunc("GET /api/config", h.config)
	mux.HandleFunc("POST /api/commands/{name}", h.command)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	t, err := engine.Call(r.Context(), h.Engine, h.Engine.Engine.GetStatus, &enginev1.GetStatusRequest{})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "GetStatus", err)
		return
	}
	rest.WriteJSON(w, http.StatusOK, rest.FromTelemetry(t))
}

func (h *Handler) config(w http.ResponseWriter, r *http.Request) {
	c, err := engine.Call(r.Context(), h.Engine, h.Engine.Engine.GetRobotConfig, &enginev1.GetRobotConfigRequest{})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "GetRobotConfig", err)
		return
	}
	rest.WriteJSON(w, http.StatusOK, rest.FromRobotConfig(c))
}

func (h *Handler) command(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	run, ok := commands[name]
	if !ok {
		rest.WriteError(w, http.StatusNotFound, "unknown_command", "unknown command: "+name)
		return
	}
	body, ok := rest.ReadObject(w, r)
	if !ok {
		return
	}
	err := run(r.Context(), h.Engine, body)
	var pe *paramError
	switch {
	case errors.As(err, &pe):
		rest.BadRequest(w, pe.msg)
	case err != nil:
		rest.WriteEngineError(w, h.Log, name, err)
	default:
		rest.WriteJSON(w, http.StatusOK, struct{}{})
	}
}

// paramError is a request validation failure (400 bad_request).
type paramError struct{ msg string }

func (e *paramError) Error() string { return e.msg }

func badParams(format string, args ...any) error {
	return &paramError{fmt.Sprintf(format, args...)}
}

// command validates params (a JSON object) and performs the engine call.
type command func(ctx context.Context, e *engine.Client, params json.RawMessage) error

// commands maps REST command names to their implementation.
var commands = map[string]command{
	"enable": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.Enable, &enginev1.EnableRequest{}))
	}),
	"disable": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.Disable, &enginev1.DisableRequest{}))
	}),
	"stop": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.Stop, &enginev1.StopRequest{}))
	}),
	"estop": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.EmergencyStop, &enginev1.EmergencyStopRequest{}))
	}),
	"reset_fault": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.ResetFault, &enginev1.ResetFaultRequest{}))
	}),
	"jog": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			Axis     *string  `json:"axis"`
			Delta    *float64 `json:"delta"`
			Velocity *float64 `json:"velocity"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		if err := requireAxis(p.Axis); err != nil {
			return err
		}
		if err := requireNumber("delta", p.Delta); err != nil {
			return err
		}
		if err := optionalVelocity(p.Velocity); err != nil {
			return err
		}
		return discard(engine.Call(ctx, e, e.Engine.Jog,
			&enginev1.JogRequest{Axis: *p.Axis, Delta: *p.Delta, Velocity: p.Velocity}))
	},
	"move_to": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			Axis     *string  `json:"axis"`
			Position *float64 `json:"position"`
			Velocity *float64 `json:"velocity"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		if err := requireAxis(p.Axis); err != nil {
			return err
		}
		if err := requireNumber("position", p.Position); err != nil {
			return err
		}
		if err := optionalVelocity(p.Velocity); err != nil {
			return err
		}
		return discard(engine.Call(ctx, e, e.Engine.MoveTo,
			&enginev1.MoveToRequest{Axis: *p.Axis, Position: *p.Position, Velocity: p.Velocity}))
	},
	"home": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			Axis *string `json:"axis"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		if err := requireAxis(p.Axis); err != nil {
			return err
		}
		return discard(engine.Call(ctx, e, e.Engine.Home, &enginev1.HomeRequest{Axis: *p.Axis}))
	},
}

// Names returns the supported command names, sorted.
func Names() []string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func noParams(f func(context.Context, *engine.Client) error) command {
	return func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		if err := decode(raw, &struct{}{}); err != nil {
			return err
		}
		return f(ctx, e)
	}
}

func discard[T any](_ T, err error) error { return err }

// decode decodes params strictly (unknown fields are rejected).
func decode(raw json.RawMessage, dst any) error {
	if err := rest.Decode(raw, dst); err != nil {
		return badParams("%v", err)
	}
	return nil
}

func requireAxis(axis *string) error {
	if axis == nil || *axis == "" {
		return badParams(`missing parameter "axis"`)
	}
	return nil
}

func requireNumber(name string, v *float64) error {
	if v == nil {
		return badParams("missing parameter %q", name)
	}
	return nil
}

// optionalVelocity checks the optional velocity; range checks against the
// axis limits are the engine's job.
func optionalVelocity(v *float64) error {
	if v != nil && (*v <= 0 || math.IsInf(*v, 0) || math.IsNaN(*v)) {
		return badParams(`"velocity" must be positive`)
	}
	return nil
}
