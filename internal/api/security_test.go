package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/bzy-logics/ratavyr/internal/audit"
	"github.com/bzy-logics/ratavyr/internal/auth"
)

var roleTokens = auth.Tokens{
	Admin:    "admin-token-0123456789",
	Operator: "operator-token-0123456789",
	Viewer:   "viewer-token-0123456789",
}

func bearer(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

func TestRolesAreEnforced(t *testing.T) {
	e := setup(t, setupOpts{tokens: roleTokens})
	type check struct {
		method, path, body string
		viewer, operator   int
	}
	// Admin may do everything; flash then fails at the fake engine (404 job).
	checks := []check{
		{"GET", "/api/status", "", 200, 200},
		{"GET", "/api/config", "", 200, 200},
		{"POST", "/api/commands/estop", "{}", 200, 200},
		{"POST", "/api/commands/stop", "{}", 200, 200},
		{"POST", "/api/commands/enable", "{}", 403, 200},
		{"POST", "/api/commands/jog", `{"axis":"j1","delta":1}`, 403, 200},
		{"POST", "/api/commands/reset_fault", "{}", 403, 200},
		{"POST", "/api/commands/home_all", "{}", 403, 200},
		{"POST", "/api/commands/move_j", `{"targets":{"j1":1}}`, 403, 200},
		{"POST", "/api/firmware/builds", `{"mcu":"main"}`, 403, 202},
		{"POST", "/api/firmware/flashes", `{"build_job_id":"build-1","confirm":true}`, 403, 403},
	}
	for _, c := range checks {
		for _, rc := range []struct {
			name string
			tok  string
			want int
		}{{"viewer", roleTokens.Viewer, c.viewer}, {"operator", roleTokens.Operator, c.operator}} {
			code, b := do(t, c.method, e.srv.URL+c.path, c.body, bearer(rc.tok)...)
			if code != rc.want {
				t.Errorf("%s %s as %s: %d %s (want %d)", c.method, c.path, rc.name, code, b, rc.want)
			}
			if code == 403 && errCode(t, b) != "forbidden" {
				t.Errorf("%s %s as %s: code %s", c.method, c.path, rc.name, errCode(t, b))
			}
		}
	}
	if code, b := do(t, "POST", e.srv.URL+"/api/firmware/flashes", `{"build_job_id":"build-1","confirm":true}`, bearer(roleTokens.Admin)...); code == 403 || code == 401 {
		t.Errorf("admin flash: %d %s", code, b)
	}
}

func TestSessionDescribesRole(t *testing.T) {
	e := setup(t, setupOpts{tokens: roleTokens})
	for tok, want := range map[string]string{
		roleTokens.Viewer:   `{"auth_required":true,"role":"viewer","permissions":{"view":true,"stop":true,"command":false,"firmware_build":false,"firmware_flash":false}}`,
		roleTokens.Operator: `{"auth_required":true,"role":"operator","permissions":{"view":true,"stop":true,"command":true,"firmware_build":true,"firmware_flash":false}}`,
		roleTokens.Admin:    `{"auth_required":true,"role":"admin","permissions":{"view":true,"stop":true,"command":true,"firmware_build":true,"firmware_flash":true}}`,
	} {
		_, b := do(t, "GET", e.srv.URL+"/api/session", "", bearer(tok)...)
		assertJSON(t, "session", b, want)
	}
	if code, _ := do(t, "GET", e.srv.URL+"/api/session", ""); code != 401 {
		t.Errorf("anonymous session: %d", code)
	}
	// Without tokens everything is allowed (loopback only).
	open := setup(t, setupOpts{})
	_, b := do(t, "GET", open.srv.URL+"/api/session", "")
	assertJSON(t, "open session", b, `{"auth_required":false,"role":"admin","permissions":{"view":true,"stop":true,"command":true,"firmware_build":true,"firmware_flash":true}}`)
}

func ticket(t *testing.T, base, tok string) string {
	t.Helper()
	code, b := do(t, "POST", base+"/api/ws-tickets", "{}", append(bearer(tok), "Content-Type", "application/json")...)
	if code != 201 {
		t.Fatalf("ticket: %d %s", code, b)
	}
	var r struct {
		Ticket      string `json:"ticket"`
		ExpiresInMS int64  `json:"expires_in_ms"`
	}
	if err := json.Unmarshal(b, &r); err != nil || len(r.Ticket) < 40 || r.ExpiresInMS != 30_000 {
		t.Fatalf("ticket body %s", b)
	}
	return r.Ticket
}

func TestWebSocketTicketsAreSingleUse(t *testing.T) {
	e := setup(t, setupOpts{tokens: roleTokens})
	tk := ticket(t, e.srv.URL, roleTokens.Viewer)
	ws := dial(t, e.srv.URL, "?ticket="+tk)
	if m := readMsg(t, ws); m.Type != "engine" {
		t.Fatalf("ws: %+v", m)
	}
	if _, err := tryDial(e.srv.URL, "?ticket="+tk); err == nil {
		t.Error("ticket accepted twice")
	}
	if _, err := tryDial(e.srv.URL, "?ticket=forged"); err == nil {
		t.Error("forged ticket accepted")
	}
	// Tickets are only for the WebSocket.
	if code, _ := do(t, "GET", e.srv.URL+"/api/status?ticket="+ticket(t, e.srv.URL, roleTokens.Viewer), ""); code != 401 {
		t.Errorf("ticket on REST: %d", code)
	}
	// Legacy query token still works (deprecated).
	legacy := dial(t, e.srv.URL, "?access_token="+roleTokens.Viewer)
	if m := readMsg(t, legacy); m.Type != "engine" {
		t.Fatalf("legacy ws: %+v", m)
	}
}

func TestCrossSiteRequestsAreRejected(t *testing.T) {
	e := setup(t, setupOpts{}) // auth off: the CSRF-sensitive configuration
	cases := []struct {
		name   string
		header []string
		want   int
		code   string
	}{
		{"form post from evil origin", []string{"Origin", "https://evil.example", "Content-Type", "text/plain"}, 403, "forbidden"},
		{"json from evil origin", []string{"Origin", "https://evil.example", "Content-Type", "application/json"}, 403, "forbidden"},
		{"text/plain without origin", []string{"Content-Type", "text/plain"}, 415, "unsupported_media_type"},
		{"urlencoded", []string{"Content-Type", "application/x-www-form-urlencoded"}, 415, "unsupported_media_type"},
		{"rebinding host", []string{"Host", "attacker.example:8080"}, 403, "forbidden"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/commands/enable", strings.NewReader("{}"))
		for i := 0; i+1 < len(c.header); i += 2 {
			if c.header[i] == "Host" {
				req.Host = c.header[i+1]
			} else {
				req.Header.Set(c.header[i], c.header[i+1])
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.want || errCode(t, body.Bytes()) != c.code {
			t.Errorf("%s: %d %s", c.name, resp.StatusCode, body.String())
		}
	}
	if calls(e, "Enable") != 0 {
		t.Fatal("a rejected request reached the engine")
	}
	// Same origin, the dev-server origin and non-browser clients work.
	for _, h := range [][]string{
		{"Origin", e.srv.URL, "Content-Type", "application/json"},
		{"Origin", "http://localhost:5173", "Content-Type", "application/json"},
		{"Content-Type", "application/json; charset=utf-8"},
		nil,
	} {
		if code, b := do(t, "POST", e.srv.URL+"/api/commands/enable", "{}", h...); code != 200 {
			t.Errorf("legit request %v: %d %s", h, code, b)
		}
	}
	if calls(e, "Enable") != 4 {
		t.Fatalf("legit requests reaching the engine: %d (calls %v)", calls(e, "Enable"), e.fake.Calls())
	}
	// An explicitly allowed host (e.g. a LAN name behind a proxy).
	lan := setup(t, setupOpts{hosts: []string{"robot.lan:*"}})
	req, _ := http.NewRequest("GET", lan.srv.URL+"/api/status", nil)
	req.Host = "robot.lan:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("allowed host: %v %v", err, resp)
	}
	resp.Body.Close()
}

func TestRateLimitNeverBlocksStopping(t *testing.T) {
	e := setup(t, setupOpts{rate: 0.001, burst: 2})
	codes := []int{}
	for range 4 {
		code, _ := do(t, "POST", e.srv.URL+"/api/commands/jog", `{"axis":"j1","delta":1}`)
		codes = append(codes, code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Fatalf("jog codes %v", codes)
	}
	for i := range 10 {
		for _, cmd := range []string{"estop", "stop"} {
			if code, b := do(t, "POST", e.srv.URL+"/api/commands/"+cmd, "{}"); code != 200 {
				t.Fatalf("%s #%d limited: %d %s", cmd, i, code, b)
			}
		}
	}
	// Reads are not limited.
	if code, _ := do(t, "GET", e.srv.URL+"/api/status", ""); code != 200 {
		t.Fatalf("status: %d", code)
	}
}

func TestRepeatedAuthFailuresAreBlocked(t *testing.T) {
	e := setup(t, setupOpts{tokens: roleTokens})
	for range auth.MaxAuthFailures {
		if code, _ := do(t, "GET", e.srv.URL+"/api/status", "", bearer("guess")...); code != 401 {
			t.Fatalf("guess: %d", code)
		}
	}
	code, b := do(t, "GET", e.srv.URL+"/api/status", "", bearer(roleTokens.Admin)...)
	if code != 429 || errCode(t, b) != "rate_limited" {
		t.Fatalf("after %d failures: %d %s", auth.MaxAuthFailures, code, b)
	}
	// Public endpoints stay reachable.
	if code, _ := do(t, "GET", e.srv.URL+"/api/health", ""); code != 200 {
		t.Fatalf("health: %d", code)
	}
}

func TestWrongMethodIs405(t *testing.T) {
	e := setup(t, setupOpts{})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET" || errCode(t, body.Bytes()) != "method_not_allowed" {
		t.Fatalf("POST /api/status: %d allow=%q %s", resp.StatusCode, resp.Header.Get("Allow"), body.String())
	}
	if code, b := do(t, "GET", e.srv.URL+"/api/nope", ""); code != 404 || errCode(t, b) != "not_found" {
		t.Fatalf("unknown: %d %s", code, b)
	}
}

func TestInvalidAxisIDRejected(t *testing.T) {
	e := setup(t, setupOpts{})
	for _, axis := range []string{"j1;rm", strings.Repeat("a", 65), "../x", "j 1"} {
		body, _ := json.Marshal(map[string]any{"axis": axis, "delta": 1})
		if code, b := do(t, "POST", e.srv.URL+"/api/commands/jog", string(body)); code != 400 {
			t.Errorf("axis %q: %d %s", axis, code, b)
		}
	}
	if calls(e, "Jog") != 0 {
		t.Fatal("invalid axis reached the engine")
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) lines() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func TestAuditRecordsCommandsAndRejections(t *testing.T) {
	buf := &syncBuf{}
	e := setup(t, setupOpts{tokens: roleTokens, audit: audit.New(slog.New(slog.NewJSONHandler(buf, nil)))})
	do(t, "POST", e.srv.URL+"/api/commands/jog", `{"axis":"j1","delta":2.5}`, bearer(roleTokens.Operator)...)
	do(t, "POST", e.srv.URL+"/api/commands/enable", `{}`, bearer(roleTokens.Viewer)...)
	do(t, "GET", e.srv.URL+"/api/status", "", bearer("wrong")...)
	do(t, "GET", e.srv.URL+"/api/status", "", bearer(roleTokens.Viewer)...) // reads are not audited

	recs := buf.lines()
	if len(recs) != 3 {
		t.Fatalf("records: %v", recs)
	}
	jog := recs[0]
	if jog["action"] != "command.jog" || jog["role"] != "operator" || jog["outcome"] != "ok" || jog["status"] != float64(200) ||
		jog["params"] != `{"axis":"j1","delta":2.5}` || jog["remote"] != "127.0.0.1" {
		t.Errorf("jog record: %v", jog)
	}
	if r := recs[1]; r["action"] != "command.enable" || r["role"] != "viewer" || r["outcome"] != "rejected" || r["status"] != float64(403) {
		t.Errorf("forbidden record: %v", r)
	}
	if r := recs[2]; r["action"] != "auth.rejected" || r["role"] != "none" {
		t.Errorf("auth record: %v", r)
	}
}

func TestInfoExplainsIncompatibleEngine(t *testing.T) {
	e := setup(t, setupOpts{apiVersion: "1.2.0"})
	var info struct {
		Engine      any     `json:"engine"`
		EngineError *string `json:"engine_error"`
	}
	for range 200 {
		_, b := do(t, "GET", e.srv.URL+"/api/info", "")
		json.Unmarshal(b, &info)
		if info.EngineError != nil {
			break
		}
	}
	if info.Engine != nil || info.EngineError == nil || !strings.Contains(*info.EngineError, `"1.2.0"`) {
		t.Fatalf("info: engine=%v error=%v", info.Engine, info.EngineError)
	}
}

// calls counts engine RPCs whose method name ends in name.
func calls(e *env, name string) int {
	n := 0
	for _, c := range e.fake.Calls() {
		if strings.HasSuffix(c, "/"+name) || c == name {
			n++
		}
	}
	return n
}
