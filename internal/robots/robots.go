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

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzy-logics/ratavyr/internal/api/rest"
	"github.com/bzy-logics/ratavyr/internal/audit"
	"github.com/bzy-logics/ratavyr/internal/auth"
	"github.com/bzy-logics/ratavyr/internal/engine"
)

// RoleFor is the minimum role for a command. Stopping is open to every
// authenticated client (anyone who can see the robot may stop it); anything
// that energises drives or moves an axis needs the operator role.
func RoleFor(command string) auth.Role {
	switch command {
	case "stop", "estop":
		return auth.Viewer
	default:
		return auth.Operator
	}
}

// MaxAxisIDLength bounds axis identifiers in requests.
const MaxAxisIDLength = 64

// MaxJointTargets bounds the axes of one move_j command (and of each
// move_j_path waypoint).
const MaxJointTargets = 32

// MaxWaypoints bounds the waypoints of one move_j_path command (as the engine).
const MaxWaypoints = 256

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
	if need := RoleFor(name); auth.RoleFrom(r.Context()) < need {
		rest.WriteError(w, http.StatusForbidden, "forbidden",
			"command "+name+" requires the "+need.String()+" role (you are "+auth.RoleFrom(r.Context()).String()+")")
		return
	}
	body, ok := rest.ReadObject(w, r)
	if !ok {
		return
	}
	audit.Note(r.Context(), "params", string(body))
	err := run(r.Context(), h.Engine, body)
	var pe *paramError
	var ee *engine.Error
	switch {
	case errors.As(err, &pe):
		audit.Note(r.Context(), "error", "bad_request")
		rest.BadRequest(w, pe.msg)
	case err != nil:
		code := "engine_unavailable"
		if errors.As(err, &ee) {
			code = ee.Code
		} else if errors.Is(err, engine.ErrTimeout) {
			code = "engine_timeout"
		}
		audit.Note(r.Context(), "error", code)
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
	"move_j": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			Targets    map[string]float64 `json:"targets"`
			SpeedScale *float64           `json:"speed_scale"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		if err := optionalSpeedScale(p.SpeedScale); err != nil {
			return err
		}
		targets, err := jointTargets(p.Targets)
		if err != nil {
			return badParams("%v", err)
		}
		return discard(engine.Call(ctx, e, e.Engine.MoveJ, &enginev1.MoveJRequest{Targets: targets, SpeedScale: p.SpeedScale}))
	},
	"move_j_path": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			Waypoints  []map[string]float64 `json:"waypoints"`
			SpeedScale *float64             `json:"speed_scale"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		if len(p.Waypoints) == 0 || len(p.Waypoints) > MaxWaypoints {
			return badParams(`"waypoints" must list 1 to %d joint maps`, MaxWaypoints)
		}
		if err := optionalSpeedScale(p.SpeedScale); err != nil {
			return err
		}
		req := &enginev1.MoveJPathRequest{SpeedScale: p.SpeedScale}
		for n, w := range p.Waypoints {
			targets, err := jointTargets(w)
			if err != nil {
				return badParams("waypoint %d: %v", n+1, err)
			}
			req.Waypoints = append(req.Waypoints, &enginev1.JointWaypoint{Targets: targets})
		}
		return discard(engine.Call(ctx, e, e.Engine.MoveJPath, req))
	},
	"move_l": func(ctx context.Context, e *engine.Client, raw json.RawMessage) error {
		var p struct {
			PositionMM  *[]float64 `json:"position_mm"`
			RotationDeg *[]float64 `json:"rotation_deg"`
			SpeedMMS    *float64   `json:"speed_mm_s"`
			SpeedScale  *float64   `json:"speed_scale"`
		}
		if err := decode(raw, &p); err != nil {
			return err
		}
		pos, err := vec3("position_mm", p.PositionMM, true)
		if err != nil {
			return err
		}
		rot, err := vec3("rotation_deg", p.RotationDeg, false)
		if err != nil {
			return err
		}
		if s := p.SpeedMMS; s != nil && !(*s > 0 && *s <= 2000) {
			return badParams(`"speed_mm_s" must be in (0, 2000]`)
		}
		if err := optionalSpeedScale(p.SpeedScale); err != nil {
			return err
		}
		return discard(engine.Call(ctx, e, e.Engine.MoveL,
			&enginev1.MoveLRequest{PositionMm: pos, RotationDeg: rot, SpeedMmS: p.SpeedMMS, SpeedScale: p.SpeedScale}))
	},
	"home_all": noParams(func(ctx context.Context, e *engine.Client) error {
		return discard(engine.Call(ctx, e, e.Engine.HomeAll, &enginev1.HomeAllRequest{}))
	}),
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
	if len(*axis) > MaxAxisIDLength {
		return badParams(`"axis" is longer than %d characters`, MaxAxisIDLength)
	}
	for _, c := range *axis {
		if !(c == '_' || c == '-' || c == '.' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')) {
			return badParams(`"axis" may only contain letters, digits, '_', '-' and '.'`)
		}
	}
	return nil
}

func requireNumber(name string, v *float64) error {
	if v == nil {
		return badParams("missing parameter %q", name)
	}
	return nil
}

// jointTargets validates an axis → position map (1 to MaxJointTargets
// entries, valid axis ids) and returns it sorted by axis id.
func jointTargets(m map[string]float64) ([]*enginev1.JointTarget, error) {
	if len(m) == 0 {
		return nil, fmt.Errorf(`"targets" must map at least one axis to a position`)
	}
	if len(m) > MaxJointTargets {
		return nil, fmt.Errorf(`"targets" lists more than %d axes`, MaxJointTargets)
	}
	axes := make([]string, 0, len(m))
	for a := range m {
		axes = append(axes, a)
	}
	sort.Strings(axes)
	out := make([]*enginev1.JointTarget, 0, len(axes))
	for _, a := range axes {
		if err := requireAxis(&a); err != nil {
			return nil, err
		}
		out = append(out, &enginev1.JointTarget{Axis: a, Position: m[a]})
	}
	return out, nil
}

// optionalSpeedScale checks an optional fraction in (0, 1].
func optionalSpeedScale(s *float64) error {
	if s != nil && !(*s > 0 && *s <= 1) {
		return badParams(`"speed_scale" must be in (0, 1]`)
	}
	return nil
}

// vec3 checks an [x, y, z] array.
func vec3(name string, v *[]float64, required bool) (*enginev1.Vec3, error) {
	if v == nil {
		if required {
			return nil, badParams("missing parameter %q", name)
		}
		return nil, nil
	}
	if len(*v) != 3 {
		return nil, badParams("%q must be an array of 3 numbers", name)
	}
	return &enginev1.Vec3{X: (*v)[0], Y: (*v)[1], Z: (*v)[2]}, nil
}

// optionalVelocity checks the optional velocity; range checks against the
// axis limits are the engine's job.
func optionalVelocity(v *float64) error {
	if v != nil && (*v <= 0 || math.IsInf(*v, 0) || math.IsNaN(*v)) {
		return badParams(`"velocity" must be positive`)
	}
	return nil
}
