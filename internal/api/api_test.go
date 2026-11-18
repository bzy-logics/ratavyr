package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzy-logics/ratavyr/internal/api"
	"github.com/bzy-logics/ratavyr/internal/audit"
	"github.com/bzy-logics/ratavyr/internal/auth"
	"github.com/bzy-logics/ratavyr/internal/engine"
	"github.com/bzy-logics/ratavyr/internal/engine/enginetest"
	"github.com/coder/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

type env struct {
	fake *enginetest.Engine
	eng  *engine.Client
	srv  *httptest.Server
}

type setupOpts struct {
	handler    enginetest.HandlerFunc
	noEngine   bool   // point the client at a closed port
	apiVersion string // engine api_version (default 0.1.4)
	token      string
	webDir     string
	tokens     auth.Tokens
	hosts      []string
	rate       float64
	burst      float64
	audit      *audit.Logger
}

func setup(t *testing.T, o setupOpts) *env {
	t.Helper()
	fake := enginetest.New(t, o.handler)
	if o.apiVersion != "" {
		fake.Info.ApiVersion = o.apiVersion
	}
	addr := fake.Addr()
	if o.noEngine {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr = l.Addr().String()
		l.Close()
	}
	eng, err := engine.New(engine.Options{Target: addr, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.Run(ctx); close(done) }()
	srv := httptest.NewServer(api.New(api.Options{
		Engine: eng, Token: o.token, Tokens: o.tokens, WebDir: o.webDir, Audit: o.audit,
		OriginPatterns: []string{"localhost:5173"}, AllowedHosts: o.hosts, RateLimit: o.rate, RateBurst: o.burst,
	}))
	t.Cleanup(func() { srv.Close(); cancel(); <-done; eng.Close() })
	if !o.noEngine && o.apiVersion == "" {
		deadline := time.Now().Add(5 * time.Second)
		for !eng.Connected() {
			if time.Now().After(deadline) {
				t.Fatal("engine did not connect")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return &env{fake, eng, srv}
}

func do(t *testing.T, method, url, body string, header ...string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type apiError struct {
	Error struct{ Code, Message string }
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var e apiError
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("bad error body %q: %v", b, err)
	}
	return e.Error.Code
}

// assertJSON compares JSON documents semantically.
func assertJSON(t *testing.T, what string, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: invalid JSON %q: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: invalid expected JSON: %v", what, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("%s:\n got  %s\n want %s", what, gb, wb)
	}
}

func ptr[T any](v T) *T { return &v }

var fullTelemetry = &enginev1.Telemetry{
	Seq: 1042, TimeMs: 52100, State: enginev1.RobotState_ROBOT_STATE_EMERGENCY_STOPPED,
	Fault: &enginev1.Fault{Code: enginev1.FaultCode_FAULT_CODE_COMMUNICATION_LOST, Message: "link lost", Axis: "j1"},
	Mcu: &enginev1.McuTelemetry{
		Connected: true, Transport: "virtual", Target: "virtual", Board: "virtual", FirmwareVersion: "0.1.0",
		ProtocolVersion: 1, BuildHash: "51570001", Capabilities: []string{"endstop"},
		QueueCapacity: 32, QueueDepth: 1, ClockOffsetUs: -1520, RttUs: 180,
		TxFrames: 2201, RxFrames: 2199, CrcErrors: 3, LastHeartbeatMsAgo: ptr[uint64](12),
	},
	Axes: []*enginev1.AxisTelemetry{{Id: "j1", Homed: true, Enabled: true, CommandedPosition: 45, MeasuredPosition: 44.98, Velocity: 1.5, Endstop: true}},
}

const fullTelemetryJSON = `{
 "seq":1042,"time_ms":52100,"state":"emergency_stopped",
 "fault":{"code":"communication_lost","message":"link lost","axis":"j1"},
 "mcu":{"connected":true,"transport":"virtual","target":"virtual","board":"virtual","firmware_version":"0.1.0",
  "protocol_version":1,"build_hash":"51570001","capabilities":["endstop"],"queue_capacity":32,"queue_depth":1,
  "clock_offset_us":-1520,"rtt_us":180,"tx_frames":2201,"rx_frames":2199,"crc_errors":3,"last_heartbeat_ms_ago":12},
 "axes":[{"id":"j1","homed":true,"enabled":true,"commanded_position":45,"measured_position":44.98,"velocity":1.5,"endstop":true,"homing":false}]
}`

func TestStatusMapping(t *testing.T) {
	var minimal bool
	var mu sync.Mutex
	e := setup(t, setupOpts{handler: func(_ context.Context, method string, _ proto.Message) (proto.Message, error) {
		mu.Lock()
		defer mu.Unlock()
		if minimal {
			// Fault unset, no MCU link, axis fault without axis.
			return &enginev1.Telemetry{State: enginev1.RobotState_ROBOT_STATE_READY}, nil
		}
		return fullTelemetry, nil
	}})
	code, b := do(t, "GET", e.srv.URL+"/api/status", "")
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	assertJSON(t, "full", b, fullTelemetryJSON)

	mu.Lock()
	minimal = true
	mu.Unlock()
	_, b = do(t, "GET", e.srv.URL+"/api/status", "")
	assertJSON(t, "minimal", b, `{"seq":0,"time_ms":0,"state":"ready","fault":null,
	 "mcu":{"connected":false,"transport":"","target":"","board":"","firmware_version":"","protocol_version":0,
	  "build_hash":"","capabilities":[],"queue_capacity":0,"queue_depth":0,"clock_offset_us":0,"rtt_us":0,
	  "tx_frames":0,"rx_frames":0,"crc_errors":0,"last_heartbeat_ms_ago":null},"axes":[]}`)
}

func TestFaultAxisNull(t *testing.T) {
	e := setup(t, setupOpts{handler: func(context.Context, string, proto.Message) (proto.Message, error) {
		return &enginev1.Telemetry{
			State: enginev1.RobotState_ROBOT_STATE_FAULTED,
			Fault: &enginev1.Fault{Code: enginev1.FaultCode_FAULT_CODE_PLUGIN_FAILED, Message: "x"},
		}, nil
	}})
	_, b := do(t, "GET", e.srv.URL+"/api/status", "")
	var got struct {
		State string          `json:"state"`
		Fault json.RawMessage `json:"fault"`
	}
	json.Unmarshal(b, &got)
	if got.State != "faulted" {
		t.Errorf("state %q", got.State)
	}
	assertJSON(t, "fault", got.Fault, `{"code":"plugin_failed","message":"x","axis":null}`)
}

func TestConfigAndPluginsMapping(t *testing.T) {
	e := setup(t, setupOpts{handler: func(_ context.Context, method string, _ proto.Message) (proto.Message, error) {
		switch method {
		case "GetRobotConfig":
			return &enginev1.RobotConfig{
				Name: "bench", Profile: "org.motioncore.robot.single_axis",
				Mcus: []*enginev1.McuConfig{{Name: "main", Board: "virtual", Transport: "virtual", WatchdogMs: 500}},
				Axes: []*enginev1.AxisConfig{
					{Id: "j1", Name: "Base", Kind: enginev1.JointKind_JOINT_KIND_REVOLUTE, Unit: "deg", Min: -170, Max: 170,
						MaxVelocity: 90, MaxAcceleration: 360, HomePosition: ptr(-170.0), StepsPerUnit: 44.444, Mcu: "main",
						Actuator: "org.motioncore.actuator.step_dir", StepPin: "STEP0", DirPin: "DIR0", EnablePin: "EN0", EndstopPin: "ENDSTOP0",
						Geometry: &enginev1.AxisGeometry{OriginMm: &enginev1.Vec3{Z: 120}, Axis: &enginev1.Vec3{Z: 1}, RadiusMm: ptr(30.0)}},
					{Id: "z", Kind: enginev1.JointKind_JOINT_KIND_PRISMATIC, Unit: "mm", McuChannel: 1,
						Geometry: &enginev1.AxisGeometry{Parent: "j1", OriginMm: &enginev1.Vec3{X: 250}, RotationDeg: &enginev1.Vec3{Y: 90}, Axis: &enginev1.Vec3{X: 1}, TipMm: &enginev1.Vec3{X: 50}}},
					{Id: "plain", Unit: "deg"},
				},
				HomeAll: &enginev1.HomeAllPlan{
					Groups:      []*enginev1.HomeAllGroup{{Axes: []string{"j1"}}, {Axes: []string{"j2", "z"}}},
					StartPose:   "arm upright",
					FromProfile: true,
				},
				CollisionChecking: true,
			}, nil
		case "ListPlugins":
			return &enginev1.ListPluginsResponse{Plugins: []*enginev1.Plugin{
				{Id: "a", Version: "0.2.0", ApiVersion: "0", Kind: "builtin", Provides: []string{"robot.profile"}, State: enginev1.PluginState_PLUGIN_STATE_ACTIVE},
				{Id: "b", Kind: "wasm", State: enginev1.PluginState_PLUGIN_STATE_FAILED, Error: "trap"},
				{Id: "c", State: enginev1.PluginState(99)},
			}}, nil
		}
		return nil, nil
	}})
	_, b := do(t, "GET", e.srv.URL+"/api/config", "")
	assertJSON(t, "config", b, `{"name":"bench","profile":"org.motioncore.robot.single_axis",
	 "mcus":[{"name":"main","board":"virtual","transport":"virtual","watchdog_ms":500}],
	 "axes":[
	  {"id":"j1","name":"Base","kind":"revolute","unit":"deg","min":-170,"max":170,"max_velocity":90,"max_acceleration":360,
	   "home_position":-170,"steps_per_unit":44.444,"mcu":"main","mcu_channel":0,"actuator":"org.motioncore.actuator.step_dir",
	   "step_pin":"STEP0","dir_pin":"DIR0","enable_pin":"EN0","endstop_pin":"ENDSTOP0",
	   "geometry":{"parent":null,"origin_mm":[0,0,120],"rotation_deg":[0,0,0],"axis":[0,0,1],"tip_mm":null,"radius_mm":30}},
	  {"id":"z","name":"","kind":"prismatic","unit":"mm","min":0,"max":0,"max_velocity":0,"max_acceleration":0,
	   "home_position":null,"steps_per_unit":0,"mcu":"","mcu_channel":1,"actuator":"",
	   "step_pin":"","dir_pin":"","enable_pin":"","endstop_pin":null,
	   "geometry":{"parent":"j1","origin_mm":[250,0,0],"rotation_deg":[0,90,0],"axis":[1,0,0],"tip_mm":[50,0,0],"radius_mm":null}},
	  {"id":"plain","name":"","kind":"unspecified","unit":"deg","min":0,"max":0,"max_velocity":0,"max_acceleration":0,
	   "home_position":null,"steps_per_unit":0,"mcu":"","mcu_channel":0,"actuator":"",
	   "step_pin":"","dir_pin":"","enable_pin":"","endstop_pin":null,"geometry":null}],
	 "home_all":{"groups":[["j1"],["j2","z"]],"start_pose":"arm upright","from_profile":true},"collision_checking":true}`)

	_, b = do(t, "GET", e.srv.URL+"/api/plugins", "")
	assertJSON(t, "plugins", b, `[
	 {"id":"a","version":"0.2.0","api_version":"0","kind":"builtin","description":"","provides":["robot.profile"],
	  "requires":[],"permissions":[],"state":"active","error":null},
	 {"id":"b","version":"","api_version":"","kind":"wasm","description":"","provides":[],"requires":[],"permissions":[],
	  "state":"failed","error":"trap"},
	 {"id":"c","version":"","api_version":"","kind":"","description":"","provides":[],"requires":[],"permissions":[],
	  "state":"unknown","error":null}]`)
}

func TestHealthAndInfo(t *testing.T) {
	e := setup(t, setupOpts{})
	code, b := do(t, "GET", e.srv.URL+"/api/health", "")
	if code != 200 {
		t.Fatal(code)
	}
	assertJSON(t, "health", b, `{"status":"ok","engine":{"connected":true}}`)
	_, b = do(t, "GET", e.srv.URL+"/api/info", "")
	assertJSON(t, "info", b, `{"server_version":"`+api.ServerVersion+`","auth_required":false,
	 "engine":{"api_version":"0.1.4","engine_version":"0.2.0","mcu_protocol_version":1,"plugin_api_version":"0","fault_injection_enabled":false},"engine_error":null}`)
}

func TestCommands(t *testing.T) {
	got := make(chan proto.Message, 10)
	e := setup(t, setupOpts{handler: func(_ context.Context, _ string, req proto.Message) (proto.Message, error) {
		got <- req
		return nil, nil
	}})
	recv := func() proto.Message {
		select {
		case m := <-got:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("engine not called")
			return nil
		}
	}
	ok := []struct {
		name, body string
		want       proto.Message
	}{
		{"enable", "", &enginev1.EnableRequest{}},
		{"disable", "{}", &enginev1.DisableRequest{}},
		{"stop", "", &enginev1.StopRequest{}},
		{"estop", "", &enginev1.EmergencyStopRequest{}},
		{"reset_fault", " {} ", &enginev1.ResetFaultRequest{}},
		{"jog", `{"axis":"j1","delta":5.0}`, &enginev1.JogRequest{Axis: "j1", Delta: 5}},
		{"jog", `{"axis":"j1","delta":-2.5,"velocity":30}`, &enginev1.JogRequest{Axis: "j1", Delta: -2.5, Velocity: ptr(30.0)}},
		{"jog", `{"axis":"j1","delta":1,"velocity":null}`, &enginev1.JogRequest{Axis: "j1", Delta: 1}},
		{"move_to", `{"axis":"j1","position":-90}`, &enginev1.MoveToRequest{Axis: "j1", Position: -90}},
		{"move_to", `{"axis":"j1","position":0,"velocity":10}`, &enginev1.MoveToRequest{Axis: "j1", Position: 0, Velocity: ptr(10.0)}},
		{"home", `{"axis":"j2"}`, &enginev1.HomeRequest{Axis: "j2"}},
	}
	for _, c := range ok {
		code, b := do(t, "POST", e.srv.URL+"/api/commands/"+c.name, c.body)
		if code != 200 || strings.TrimSpace(string(b)) != "{}" {
			t.Errorf("%s %s: %d %s", c.name, c.body, code, b)
			continue
		}
		if m := recv(); !proto.Equal(m, c.want) {
			t.Errorf("%s %s: engine got %v, want %v", c.name, c.body, m, c.want)
		}
	}

	bad := []struct{ name, body string }{
		{"jog", `[1]`}, {"jog", `"x"`}, {"jog", `{bad`}, {"jog", `null`},
		{"jog", `{}`}, {"jog", `{"axis":"j1"}`}, {"jog", `{"delta":1}`}, {"jog", `{"axis":"","delta":1}`},
		{"jog", `{"axis":"j1","delta":"5"}`}, {"jog", `{"axis":"j1","delta":1,"velocity":0}`},
		{"jog", `{"axis":"j1","delta":1,"extra":1}`},
		{"move_to", `{"axis":"j1"}`}, {"move_to", `{"axis":1,"position":1}`},
		{"home", `{}`}, {"enable", `{"force":true}`},
	}
	for _, c := range bad {
		if code, b := do(t, "POST", e.srv.URL+"/api/commands/"+c.name, c.body); code != 400 || errCode(t, b) != "bad_request" {
			t.Errorf("%s %s: %d %s", c.name, c.body, code, b)
		}
	}
	select {
	case m := <-got:
		t.Errorf("engine called for invalid request: %v", m)
	default:
	}

	code, b := do(t, "POST", e.srv.URL+"/api/commands/self_destruct", "")
	if code != 404 || errCode(t, b) != "unknown_command" {
		t.Fatalf("unknown command: %d %s", code, b)
	}
}

func TestEngineErrorMapping(t *testing.T) {
	cases := []struct {
		grpc  codes.Code
		mcode string
		want  int
		code  string
	}{
		{codes.InvalidArgument, "bad_request", 400, "bad_request"},
		{codes.InvalidArgument, "validation_failed", 400, "validation_failed"},
		{codes.FailedPrecondition, "not_homed", 400, "not_homed"},
		{codes.NotFound, "unknown_axis", 404, "unknown_axis"},
		{codes.NotFound, "not_found", 404, "not_found"},
		{codes.FailedPrecondition, "invalid_state", 409, "invalid_state"},
		{codes.Unavailable, "mcu_error", 502, "mcu_error"},
		{codes.Internal, "internal", 500, "internal"},
		{codes.Internal, "something_new", 500, "something_new"},
		// Without the trailer the gRPC code decides.
		{codes.InvalidArgument, "", 400, "bad_request"},
		{codes.NotFound, "", 404, "not_found"},
		{codes.FailedPrecondition, "", 409, "invalid_state"},
		{codes.Unknown, "", 500, "internal"},
		{codes.Unavailable, "", 503, "engine_unavailable"},
		{codes.DeadlineExceeded, "", 504, "engine_timeout"},
	}
	var mu sync.Mutex
	var cur int
	e := setup(t, setupOpts{handler: func(context.Context, string, proto.Message) (proto.Message, error) {
		mu.Lock()
		defer mu.Unlock()
		c := cases[cur]
		return nil, &enginetest.Error{Code: c.grpc, MCode: c.mcode, Message: "msg " + c.mcode}
	}})
	for i, c := range cases {
		mu.Lock()
		cur = i
		mu.Unlock()
		code, b := do(t, "POST", e.srv.URL+"/api/commands/move_to", `{"axis":"j9","position":1}`)
		var body apiError
		json.Unmarshal(b, &body)
		if code != c.want || body.Error.Code != c.code {
			t.Errorf("%v/%q: got %d %s, want %d %s", c.grpc, c.mcode, code, b, c.want, c.code)
		}
		if c.mcode != "" && body.Error.Message != "msg "+c.mcode {
			t.Errorf("%v/%q: message %q", c.grpc, c.mcode, body.Error.Message)
		}
	}
}

func TestEngineUnavailable(t *testing.T) {
	e := setup(t, setupOpts{noEngine: true})
	for _, r := range [][2]string{{"GET", "/api/status"}, {"GET", "/api/config"}, {"GET", "/api/plugins"},
		{"POST", "/api/commands/stop"}, {"GET", "/api/firmware/targets"}, {"POST", "/api/firmware/builds"}} {
		code, b := do(t, r[0], e.srv.URL+r[1], "")
		if code != 503 || errCode(t, b) != "engine_unavailable" {
			t.Errorf("%s %s: %d %s", r[0], r[1], code, b)
		}
	}
	code, b := do(t, "GET", e.srv.URL+"/api/health", "")
	if code != 200 {
		t.Fatal(code)
	}
	assertJSON(t, "health", b, `{"status":"ok","engine":{"connected":false}}`)
	_, b = do(t, "GET", e.srv.URL+"/api/info", "")
	if !strings.Contains(string(b), `"engine":null`) {
		t.Errorf("info: %s", b)
	}
	ws := dial(t, e.srv.URL, "")
	if m := readMsg(t, ws); m.Type != "engine" || string(m.Data) != `{"connected":false}` {
		t.Fatalf("ws: %+v", m)
	}
}

func TestIncompatibleEngineRefused(t *testing.T) {
	e := setup(t, setupOpts{apiVersion: "0.2.1"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, b := do(t, "GET", e.srv.URL+"/api/status", "")
		if code != 503 || errCode(t, b) != "engine_unavailable" {
			t.Fatalf("%d %s", code, b)
		}
		if strings.Contains(string(b), "0.2.1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no version mismatch reported: %s", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, b := do(t, "GET", e.srv.URL+"/api/health", "")
	assertJSON(t, "health", b, `{"status":"ok","engine":{"connected":false}}`)
	_, b = do(t, "GET", e.srv.URL+"/api/info", "")
	if !strings.Contains(string(b), `"engine":null`) {
		t.Errorf("info: %s", b)
	}
	if e.fake.Streams() != 0 || len(e.fake.Calls()) != 0 {
		t.Errorf("engine used despite mismatch: streams=%d calls=%v", e.fake.Streams(), e.fake.Calls())
	}
}

type wsMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func dial(t *testing.T, base, query string) *websocket.Conn {
	t.Helper()
	c, err := tryDial(base, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func tryDial(base, query string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/ws"+query, nil)
	return c, err
}

func readMsg(t *testing.T, c *websocket.Conn) wsMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m wsMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWebSocketFanout(t *testing.T) {
	e := setup(t, setupOpts{})
	a, b := dial(t, e.srv.URL, ""), dial(t, e.srv.URL, "")
	for _, c := range []*websocket.Conn{a, b} {
		if m := readMsg(t, c); m.Type != "engine" || string(m.Data) != `{"connected":true}` {
			t.Fatalf("first message: %+v", m)
		}
		// Cached initial snapshot from the engine stream.
		if m := readMsg(t, c); m.Type != "telemetry" || !strings.Contains(string(m.Data), `"state":"disabled"`) {
			t.Fatalf("initial telemetry: %+v", m)
		}
	}

	e.fake.EmitTelemetry(fullTelemetry)
	e.fake.EmitLog(&enginev1.LogEntry{TimeMs: 1234, Level: enginev1.LogLevel_LOG_LEVEL_WARN, Message: "hi"})
	for _, c := range []*websocket.Conn{a, b} {
		m := readMsg(t, c)
		if m.Type != "telemetry" {
			t.Fatalf("telemetry: %+v", m)
		}
		assertJSON(t, "ws telemetry", m.Data, fullTelemetryJSON)
		m = readMsg(t, c)
		if m.Type != "log" {
			t.Fatalf("log: %+v", m)
		}
		assertJSON(t, "ws log", m.Data, `{"time_ms":1234,"level":"warn","message":"hi"}`)
	}

	// A late client gets status plus the cached latest telemetry.
	late := dial(t, e.srv.URL, "")
	readMsg(t, late)
	if m := readMsg(t, late); m.Type != "telemetry" || !strings.Contains(string(m.Data), `"seq":1042`) {
		t.Fatalf("cached telemetry: %+v", m)
	}

	// Engine loss is pushed to clients, then the reconnect.
	e.fake.DropStreams()
	if m := readMsg(t, a); m.Type != "engine" || string(m.Data) != `{"connected":false}` {
		t.Fatalf("disconnect: %+v", m)
	}
	if m := readMsg(t, a); m.Type != "engine" || string(m.Data) != `{"connected":true}` {
		t.Fatalf("reconnect: %+v", m)
	}
}

func TestAuth(t *testing.T) {
	const tok = "s3cret"
	e := setup(t, setupOpts{token: tok})

	// Public endpoints.
	if code, _ := do(t, "GET", e.srv.URL+"/api/health", ""); code != 200 {
		t.Errorf("health: %d", code)
	}
	code, b := do(t, "GET", e.srv.URL+"/api/info", "")
	if code != 200 || !strings.Contains(string(b), `"auth_required":true`) {
		t.Errorf("info: %d %s", code, b)
	}

	for _, h := range [][]string{nil, {"Authorization", "Bearer wrong"}, {"Authorization", "Basic " + tok}, {"Authorization", "Bearer"}} {
		code, b := do(t, "GET", e.srv.URL+"/api/status", "", h...)
		if code != 401 || errCode(t, b) != "unauthorized" {
			t.Errorf("status with %v: %d %s", h, code, b)
		}
	}
	// Query tokens are only accepted on the WebSocket endpoint.
	if code, _ := do(t, "GET", e.srv.URL+"/api/status?access_token="+tok, ""); code != 401 {
		t.Errorf("status with query token: %d", code)
	}
	if code, _ := do(t, "POST", e.srv.URL+"/api/commands/stop", ""); code != 401 {
		t.Errorf("command without token: %d", code)
	}
	if code, b := do(t, "GET", e.srv.URL+"/api/status", "", "Authorization", "Bearer "+tok); code != 200 {
		t.Errorf("status with token: %d %s", code, b)
	}

	if _, err := tryDial(e.srv.URL, ""); err == nil {
		t.Error("ws without token accepted")
	}
	if _, err := tryDial(e.srv.URL, "?access_token=wrong"); err == nil {
		t.Error("ws with wrong token accepted")
	}
	ws := dial(t, e.srv.URL, "?access_token="+tok)
	if m := readMsg(t, ws); m.Type != "engine" {
		t.Fatalf("ws: %+v", m)
	}
}

func TestFirmware(t *testing.T) {
	got := make(chan proto.Message, 10)
	e := setup(t, setupOpts{handler: func(_ context.Context, method string, req proto.Message) (proto.Message, error) {
		got <- req
		switch method {
		case "ListTargets":
			return &enginev1.ListTargetsResponse{Targets: []*enginev1.FirmwareTarget{
				{Board: "stm32f411-blackpill", Description: "d", FirmwareTarget: "stm32f4", RustTarget: "thumbv7em-none-eabihf",
					StepChannels: 1, MaxStepRate: 20000, Provisional: true},
				{Board: "virtual", IsVirtual: true},
			}}, nil
		case "ListJobs":
			return &enginev1.ListJobsResponse{Jobs: []*enginev1.FirmwareJob{
				{Id: "build-1", CreatedUnix: 100, Kind: enginev1.JobKind_JOB_KIND_BUILD, State: enginev1.JobState_JOB_STATE_FAILED, FinishedUnix: 110},
				{Id: "build-2", CreatedUnix: 200, Kind: enginev1.JobKind_JOB_KIND_BUILD, State: enginev1.JobState_JOB_STATE_RUNNING},
			}}, nil
		case "GetJob":
			if req.(*enginev1.GetJobRequest).Id != "build-3" {
				return nil, &enginetest.Error{Code: codes.NotFound, MCode: "not_found", Message: "no such job"}
			}
			return &enginev1.FirmwareJob{
				Id: "build-3", Kind: enginev1.JobKind_JOB_KIND_BUILD, State: enginev1.JobState_JOB_STATE_SUCCEEDED,
				Mcu: "main", CreatedUnix: 1794765721, FinishedUnix: 1794765790, Message: "built", Log: "log",
				Artifact: &enginev1.FirmwareArtifact{Board: "stm32f411-blackpill", FirmwareVersion: "0.1.0", BuildHash: "dcdfcb82",
					ImageSha256: "7e61", Toolchain: "rustc 1.99.1", RequiredCapabilities: []string{"endstop"}},
			}, nil
		case "StartBuild":
			return &enginev1.FirmwareJob{Id: "build-4", Kind: enginev1.JobKind_JOB_KIND_BUILD, State: enginev1.JobState_JOB_STATE_QUEUED,
				Mcu: req.(*enginev1.StartBuildRequest).Mcu, CreatedUnix: 5}, nil
		case "StartFlash":
			r := req.(*enginev1.StartFlashRequest)
			if r.BuildJobId != "build-3" {
				return nil, &enginetest.Error{Code: codes.FailedPrecondition, MCode: "invalid_state", Message: "build not succeeded"}
			}
			return &enginev1.FirmwareJob{Id: "flash-1", Kind: enginev1.JobKind_JOB_KIND_FLASH, State: enginev1.JobState_JOB_STATE_QUEUED, Mcu: "main"}, nil
		}
		return nil, nil
	}})
	recv := func() proto.Message {
		select {
		case m := <-got:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("engine not called")
			return nil
		}
	}

	code, b := do(t, "GET", e.srv.URL+"/api/firmware/targets", "")
	if code != 200 {
		t.Fatalf("targets: %d %s", code, b)
	}
	recv()
	assertJSON(t, "targets", b, `[
	 {"board":"stm32f411-blackpill","description":"d","firmware_target":"stm32f4","rust_target":"thumbv7em-none-eabihf",
	  "step_channels":1,"max_step_rate":20000,"provisional":true,"is_virtual":false},
	 {"board":"virtual","description":"","firmware_target":"","rust_target":"","step_channels":0,"max_step_rate":0,
	  "provisional":false,"is_virtual":true}]`)

	_, b = do(t, "GET", e.srv.URL+"/api/firmware/jobs", "")
	recv()
	assertJSON(t, "jobs (newest first)", b, `[
	 {"id":"build-2","kind":"build","state":"running","mcu":"","created_unix":200,"finished_unix":null,"message":"","log":"","artifact":null},
	 {"id":"build-1","kind":"build","state":"failed","mcu":"","created_unix":100,"finished_unix":110,"message":"","log":"","artifact":null}]`)

	_, b = do(t, "GET", e.srv.URL+"/api/firmware/jobs/build-3", "")
	recv()
	assertJSON(t, "job", b, `{"id":"build-3","kind":"build","state":"succeeded","mcu":"main","created_unix":1794765721,
	 "finished_unix":1794765790,"message":"built","log":"log",
	 "artifact":{"board":"stm32f411-blackpill","firmware_version":"0.1.0","build_hash":"dcdfcb82","image_sha256":"7e61",
	  "toolchain":"rustc 1.99.1","required_capabilities":["endstop"]}}`)

	code, b = do(t, "GET", e.srv.URL+"/api/firmware/jobs/nope", "")
	recv()
	if code != 404 || errCode(t, b) != "not_found" {
		t.Errorf("missing job: %d %s", code, b)
	}

	for body, mcu := range map[string]string{"": "", "{}": "", `{"mcu":null}`: "", `{"mcu":"aux"}`: "aux"} {
		code, b = do(t, "POST", e.srv.URL+"/api/firmware/builds", body)
		if code != 202 {
			t.Errorf("build %q: %d %s", body, code, b)
			continue
		}
		if r := recv().(*enginev1.StartBuildRequest); r.Mcu != mcu {
			t.Errorf("build %q: engine got mcu %q", body, r.Mcu)
		}
		if !strings.Contains(string(b), `"state":"queued"`) || !strings.Contains(string(b), `"finished_unix":null`) {
			t.Errorf("build %q: %s", body, b)
		}
	}
	if code, b := do(t, "POST", e.srv.URL+"/api/firmware/builds", `{"mcu":1}`); code != 400 {
		t.Errorf("build bad mcu: %d %s", code, b)
	}

	code, b = do(t, "POST", e.srv.URL+"/api/firmware/flashes", `{"build_job_id":"build-3","confirm":true}`)
	if code != 202 || !strings.Contains(string(b), `"kind":"flash"`) {
		t.Errorf("flash: %d %s", code, b)
	}
	if r := recv().(*enginev1.StartFlashRequest); r.BuildJobId != "build-3" || !r.Confirm {
		t.Errorf("flash: engine got %v", r)
	}
	code, b = do(t, "POST", e.srv.URL+"/api/firmware/flashes", `{"build_job_id":"build-1","confirm":true}`)
	recv()
	if code != 409 || errCode(t, b) != "invalid_state" {
		t.Errorf("flash failed build: %d %s", code, b)
	}
	for _, body := range []string{"", `{}`, `{"build_job_id":"build-3"}`, `{"build_job_id":"build-3","confirm":false}`,
		`{"build_job_id":"build-3","confirm":"yes"}`, `{"confirm":true}`, `{"build_job_id":"","confirm":true}`} {
		if code, b := do(t, "POST", e.srv.URL+"/api/firmware/flashes", body); code != 400 || errCode(t, b) != "bad_request" {
			t.Errorf("flash %q: %d %s", body, code, b)
		}
	}
	select {
	case m := <-got:
		t.Errorf("engine called for invalid flash: %v", m)
	default:
	}
}

func TestStaticSPAFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("INDEX"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("JS"), 0o644)
	e := setup(t, setupOpts{webDir: dir, token: "t"})
	for path, want := range map[string]string{"/": "INDEX", "/app.js": "JS", "/robots/j1": "INDEX"} {
		if code, b := do(t, "GET", e.srv.URL+path, ""); code != 200 || string(b) != want {
			t.Errorf("%s: %d %q", path, code, b)
		}
	}
	if code, b := do(t, "GET", e.srv.URL+"/api/nope", "", "Authorization", "Bearer t"); code != 404 || errCode(t, b) != "not_found" {
		t.Errorf("/api/nope: %d %s", code, b)
	}
}

// rawPost sends a POST the way Python's urllib does ("Connection: close",
// body sent after the headers) and returns the full raw response.
func rawPost(t *testing.T, srvURL, path, auth, body string) string {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(srvURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	head := "POST " + path + " HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n" +
		"Content-Type: application/json\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n"
	if auth != "" {
		head += "Authorization: Bearer " + auth + "\r\n"
	}
	if _, err := io.WriteString(c, head+"\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond) // let the server answer before the body arrives
	if _, err := io.WriteString(c, body); err != nil {
		t.Fatal(err)
	}
	resp, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("reading response: %v (got %q)", err, resp)
	}
	return string(resp)
}

// Regression: a POST rejected before its body was read, from a client that
// sent "Connection: close", must still get the JSON error (not a reset).
func TestEarlyRejectionWithConnectionCloseDeliversError(t *testing.T) {
	const tok = "s3cret"
	e := setup(t, setupOpts{token: tok})
	cases := []struct {
		name, path, auth string
		want             []string // any of these error codes
	}{
		{"unknown command", "/api/commands/nope", tok, []string{"unknown_command"}},
		{"no such endpoint", "/api/nope", tok, []string{"not_found"}},
		// After 10 failed logins the client is rate limited: also an early
		// rejection, which must be delivered just the same.
		{"unauthorized", "/api/commands/stop", "", []string{"unauthorized", "rate_limited"}},
	}
	for _, c := range cases {
		for i := 0; i < 20; i++ {
			resp := rawPost(t, e.srv.URL, c.path, c.auth, `{"targets":{"j1":1}}`)
			ok := false
			for _, code := range c.want {
				ok = ok || strings.Contains(resp, `"code":"`+code+`"`)
			}
			if !ok {
				t.Fatalf("%s, attempt %d: response %q lacks error code %v", c.name, i, resp, c.want)
			}
		}
	}
}
