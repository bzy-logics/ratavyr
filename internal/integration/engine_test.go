//go:build integration

// Package integration runs ratavyr against the real MotionCore engine with
// its virtual MCU (the shared firmware core in simulation):
//
//	REST/WS client -> ratavyr -> gRPC -> motioncore engine -> MCU protocol -> virtual MCU
//
// Requires a built engine and the motioncore source tree:
//
//	MOTIONCORE_BIN=../motioncore/target/debug/motioncore \
//	MOTIONCORE_REPO=../motioncore \
//	go test -tags integration ./internal/integration/ -v
package integration

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzy-logics/ratavyr/internal/api"
	"github.com/bzy-logics/ratavyr/internal/auth"
	"github.com/bzy-logics/ratavyr/internal/engine"
)

var tokens = auth.Tokens{Admin: "integration-admin-token", Operator: "integration-operator-token", Viewer: "integration-viewer-token"}

type engineProc struct {
	t    *testing.T
	bin  string
	repo string
	cfg  string
	addr string
	cmd  *exec.Cmd
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func (e *engineProc) start() {
	e.t.Helper()
	e.cmd = exec.Command(e.bin, "start", "--config", e.cfg, "--listen", "tcp://"+e.addr, "--repo", e.repo, "--enable-fault-injection")
	if testing.Verbose() {
		e.cmd.Stdout, e.cmd.Stderr = os.Stderr, os.Stderr
	}
	if err := e.cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *engineProc) stop() {
	if e.cmd != nil && e.cmd.Process != nil {
		e.cmd.Process.Kill()
		e.cmd.Wait()
		e.cmd = nil
	}
}

type stack struct {
	t   *testing.T
	eng *engineProc
	cli *engine.Client
	srv *httptest.Server
}

func setup(t *testing.T) *stack { return setupProfile(t, "single-axis-virtual.toml") }

func setupProfile(t *testing.T, profile string) *stack {
	bin, repo := os.Getenv("MOTIONCORE_BIN"), os.Getenv("MOTIONCORE_REPO")
	if bin == "" || repo == "" {
		t.Skip("set MOTIONCORE_BIN and MOTIONCORE_REPO to run integration tests")
	}
	// Relative paths are relative to the ratavyr root (go test runs in the
	// package directory).
	for _, p := range []*string{&bin, &repo} {
		if !filepath.IsAbs(*p) {
			*p = filepath.Join("..", "..", *p)
		}
	}
	ep := &engineProc{t: t, bin: bin, repo: repo, cfg: filepath.Join(repo, "robot-profiles", profile), addr: freePort(t)}
	ep.start()
	cli, err := engine.New(engine.Options{Target: ep.addr, MinBackoff: 50 * time.Millisecond, MaxBackoff: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cli.Run(ctx); close(done) }()
	srv := httptest.NewServer(api.New(api.Options{Engine: cli, Tokens: tokens}))
	t.Cleanup(func() { srv.Close(); cancel(); <-done; cli.Close(); ep.stop() })
	s := &stack{t, ep, cli, srv}
	s.waitState("disabled", 30*time.Second)
	return s
}

func (s *stack) req(method, path, tok, body string) (int, []byte) {
	s.t.Helper()
	r, _ := http.NewRequest(method, s.srv.URL+path, strings.NewReader(body))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (s *stack) cmd(tok, name, params string) (int, string) {
	s.t.Helper()
	code, b := s.req("POST", "/api/commands/"+name, tok, params)
	if code == 200 {
		return code, ""
	}
	var e struct{ Error struct{ Code string } }
	json.Unmarshal(b, &e)
	return code, e.Error.Code
}

func (s *stack) mustCmd(name, params string) {
	s.t.Helper()
	if code, ec := s.cmd(tokens.Operator, name, params); code != 200 {
		s.t.Fatalf("%s %s: %d %s", name, params, code, ec)
	}
}

type telemetry struct {
	State string `json:"state"`
	Fault *struct {
		Code string `json:"code"`
	} `json:"fault"`
	MCU struct {
		Connected bool   `json:"connected"`
		Board     string `json:"board"`
	} `json:"mcu"`
	Axes []struct {
		ID        string  `json:"id"`
		Homed     bool    `json:"homed"`
		Commanded float64 `json:"commanded_position"`
		Measured  float64 `json:"measured_position"`
	} `json:"axes"`
}

func (s *stack) status() (telemetry, bool) {
	code, b := s.req("GET", "/api/status", tokens.Viewer, "")
	var t telemetry
	if code != 200 || json.Unmarshal(b, &t) != nil {
		return t, false
	}
	return t, true
}

func (s *stack) waitFor(what string, timeout time.Duration, ok func(telemetry) bool) telemetry {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	var last telemetry
	for time.Now().Before(deadline) {
		if t, got := s.status(); got {
			last = t
			if ok(t) {
				return t
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %s; last %+v", what, last)
	return last
}

func (s *stack) waitState(state string, timeout time.Duration) telemetry {
	s.t.Helper()
	return s.waitFor("state "+state, timeout, func(t telemetry) bool { return t.State == state })
}

func (s *stack) dialWS(tok string) *websocket.Conn {
	s.t.Helper()
	code, b := s.req("POST", "/api/ws-tickets", tok, "{}")
	if code != 201 {
		s.t.Fatalf("ticket: %d %s", code, b)
	}
	var tk struct{ Ticket string }
	json.Unmarshal(b, &tk)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.srv.URL, "http")+"/api/ws?ticket="+tk.Ticket, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { c.CloseNow() })
	return c
}

type wsMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// waitWS reads until a message satisfies ok.
func waitWS(t *testing.T, c *websocket.Conn, timeout time.Duration, ok func(wsMsg) bool) wsMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("websocket: %v", err)
		}
		var m wsMsg
		if json.Unmarshal(b, &m) == nil && ok(m) {
			return m
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

func TestFullMotionCycle(t *testing.T) {
	s := setup(t)
	t0, _ := s.status()
	if !t0.MCU.Connected || t0.MCU.Board != "virtual" || len(t0.Axes) != 1 || t0.Axes[0].ID != "j1" {
		t.Fatalf("initial telemetry %+v", t0)
	}
	ws := s.dialWS(tokens.Viewer)

	// Unauthorized and forbidden commands never reach the engine.
	if code, ec := s.cmd("", "enable", "{}"); code != 401 || ec != "unauthorized" {
		t.Fatalf("anonymous enable: %d %s", code, ec)
	}
	if code, ec := s.cmd(tokens.Viewer, "enable", "{}"); code != 403 || ec != "forbidden" {
		t.Fatalf("viewer enable: %d %s", code, ec)
	}
	if st, _ := s.status(); st.State != "disabled" {
		t.Fatalf("state changed by a rejected command: %s", st.State)
	}

	// Enable; absolute moves need homing (engine rule, relayed).
	s.mustCmd("enable", "{}")
	s.waitState("ready", 5*time.Second)
	if code, ec := s.cmd(tokens.Operator, "move_to", `{"axis":"j1","position":10}`); code != 400 || ec != "not_homed" {
		t.Fatalf("unhomed move_to: %d %s", code, ec)
	}
	if code, ec := s.cmd(tokens.Operator, "jog", `{"axis":"nope","delta":1}`); code != 404 || ec != "unknown_axis" {
		t.Fatalf("unknown axis: %d %s", code, ec)
	}

	// Home.
	s.mustCmd("home", `{"axis":"j1"}`)
	s.waitFor("homed", 30*time.Second, func(t telemetry) bool { return t.State == "ready" && t.Axes[0].Homed })

	// Absolute move and jog; telemetry converges on the target.
	s.mustCmd("move_to", `{"axis":"j1","position":-100}`)
	s.waitFor("move to -100", 15*time.Second, func(t telemetry) bool {
		return t.State == "ready" && near(t.Axes[0].Measured, -100) && near(t.Axes[0].Commanded, -100)
	})
	s.mustCmd("jog", `{"axis":"j1","delta":5}`)
	final := s.waitFor("jog +5", 10*time.Second, func(t telemetry) bool { return t.State == "ready" && near(t.Axes[0].Measured, -95) })

	// The WebSocket stream reports the same position as REST.
	waitWS(t, ws, 5*time.Second, func(m wsMsg) bool {
		var tel telemetry
		return m.Type == "telemetry" && json.Unmarshal(m.Data, &tel) == nil && len(tel.Axes) == 1 && near(tel.Axes[0].Measured, final.Axes[0].Measured)
	})

	// Stop during a long move.
	s.mustCmd("move_to", `{"axis":"j1","position":150}`)
	s.waitState("running", 5*time.Second)
	s.mustCmd("stop", "{}")
	stopped := s.waitState("ready", 5*time.Second)
	if near(stopped.Axes[0].Measured, 150) {
		t.Fatal("stop did not interrupt the move")
	}

	// Viewer may emergency-stop; the engine confirms via telemetry.
	if code, ec := s.cmd(tokens.Viewer, "estop", "{}"); code != 200 {
		t.Fatalf("viewer estop: %d %s", code, ec)
	}
	s.waitState("emergency_stopped", 2*time.Second)
	waitWS(t, ws, 5*time.Second, func(m wsMsg) bool {
		return m.Type == "telemetry" && strings.Contains(string(m.Data), `"state":"emergency_stopped"`)
	})
	if code, ec := s.cmd(tokens.Operator, "enable", "{}"); code != 409 || ec != "invalid_state" {
		t.Fatalf("enable while e-stopped: %d %s", code, ec)
	}
	s.mustCmd("reset_fault", "{}")
	after := s.waitState("disabled", 5*time.Second)
	if after.Axes[0].Homed {
		t.Fatal("homing must be forgotten after an emergency stop")
	}
}

func TestFaultInjectionAndReset(t *testing.T) {
	s := setup(t)
	s.mustCmd("enable", "{}")
	s.waitState("ready", 5*time.Second)
	// Simulate an MCU link loss through the engine's debug RPC (not exposed
	// by ratavyr; enabled only with --enable-fault-injection).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.cli.Engine.DebugDisconnectMcu(ctx, &enginev1.DebugDisconnectMcuRequest{}); err != nil {
		t.Fatal(err)
	}
	f := s.waitFor("fault", 10*time.Second, func(t telemetry) bool { return t.Fault != nil })
	if f.Fault.Code != "communication_lost" {
		t.Fatalf("fault %+v", f.Fault)
	}
	// The engine does not reconnect on its own while faulted: the fault
	// stays latched until an operator resets it.
	time.Sleep(3 * time.Second) // > the engine's 2 s reconnect period
	if st, _ := s.status(); st.State != "faulted" || st.MCU.Connected {
		t.Fatalf("engine recovered without a reset: %+v", st)
	}
	if code, ec := s.cmd(tokens.Operator, "enable", "{}"); code != 409 || ec != "invalid_state" {
		t.Fatalf("enable while faulted: %d %s", code, ec)
	}
	// Reset: the engine reconnects to the virtual MCU and ends up disabled.
	s.mustCmd("reset_fault", "{}")
	back := s.waitState("disabled", 15*time.Second)
	if !back.MCU.Connected {
		t.Fatalf("mcu not reconnected: %+v", back)
	}
}

func TestEngineLossAndRecovery(t *testing.T) {
	s := setup(t)
	ws := s.dialWS(tokens.Viewer)
	s.eng.stop()

	waitWS(t, ws, 10*time.Second, func(m wsMsg) bool { return m.Type == "engine" && string(m.Data) == `{"connected":false}` })
	code, b := s.req("GET", "/api/health", "", "")
	if code != 200 || !strings.Contains(string(b), `"connected":false`) {
		t.Fatalf("health while down: %d %s", code, b)
	}
	if code, ec := s.cmd(tokens.Viewer, "estop", "{}"); code != 503 || ec != "engine_unavailable" {
		t.Fatalf("estop while engine down must fail loudly: %d %s", code, ec)
	}
	_, b = s.req("GET", "/api/info", "", "")
	if !strings.Contains(string(b), `"engine":null`) || strings.Contains(string(b), `"engine_error":null`) {
		t.Fatalf("info while down: %s", b)
	}

	s.eng.start()
	waitWS(t, ws, 30*time.Second, func(m wsMsg) bool { return m.Type == "engine" && string(m.Data) == `{"connected":true}` })
	s.waitState("disabled", 30*time.Second)
	_, b = s.req("GET", "/api/info", "", "")
	if !strings.Contains(string(b), `"engine_error":null`) {
		t.Fatalf("info after recovery: %s", b)
	}
}

func TestFirmwareAPIAgainstEngine(t *testing.T) {
	s := setup(t)
	code, b := s.req("GET", "/api/firmware/targets", tokens.Viewer, "")
	if code != 200 {
		t.Fatalf("targets: %d %s", code, b)
	}
	var targets []struct {
		Board        string `json:"board"`
		StepChannels int    `json:"step_channels"`
	}
	json.Unmarshal(b, &targets)
	boards := map[string]int{}
	for _, tg := range targets {
		boards[tg.Board] = tg.StepChannels
	}
	if boards["rp2040-pico"] != 6 || boards["stm32f411-blackpill"] != 1 {
		t.Fatalf("targets: %v", boards)
	}
	// The virtual robot has no firmware to build: rejected by the engine.
	code, b = s.req("POST", "/api/firmware/builds", tokens.Operator, `{"mcu":"main"}`)
	if code != 400 {
		t.Fatalf("virtual build: %d %s", code, b)
	}
	// Flash: viewer forbidden, operator forbidden, admin reaches the engine
	// (which rejects an unknown job).
	for tok, want := range map[string]int{tokens.Viewer: 403, tokens.Operator: 403, tokens.Admin: 404} {
		if code, b := s.req("POST", "/api/firmware/flashes", tok, `{"build_job_id":"build-999","confirm":true}`); code != want {
			t.Errorf("flash: %d %s (want %d)", code, b, want)
		}
	}
	if code, _ := s.req("GET", "/api/firmware/jobs", tokens.Viewer, ""); code != 200 {
		t.Fatalf("jobs: %d", code)
	}
}

func TestGeometryIsRelayed(t *testing.T) {
	s := setupProfile(t, "rail-arm-virtual.toml")
	code, b := s.req("GET", "/api/config", tokens.Viewer, "")
	if code != 200 {
		t.Fatalf("config: %d %s", code, b)
	}
	var cfg struct {
		Axes []struct {
			ID       string `json:"id"`
			Geometry *struct {
				Parent   *string     `json:"parent"`
				OriginMM [3]float64  `json:"origin_mm"`
				Axis     [3]float64  `json:"axis"`
				TipMM    *[3]float64 `json:"tip_mm"`
			} `json:"geometry"`
		} `json:"axes"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range cfg.Axes {
		if a.Geometry == nil {
			t.Fatalf("axis %s without geometry: %s", a.ID, b)
		}
		parent := "<base>"
		if a.Geometry.Parent != nil {
			parent = *a.Geometry.Parent
		}
		got[a.ID] = parent
	}
	if got["x"] != "<base>" || got["j1"] != "x" || got["j2"] != "j1" {
		t.Fatalf("kinematic tree %v", got)
	}
	for _, a := range cfg.Axes {
		if (a.ID == "j2") != (a.Geometry.TipMM != nil) {
			t.Fatalf("tip_mm on %s: %v", a.ID, a.Geometry.TipMM)
		}
	}
	_, b = s.req("GET", "/api/info", "", "")
	if !strings.Contains(string(b), `"api_version":"1.1.0"`) {
		t.Fatalf("info: %s", b)
	}
}
