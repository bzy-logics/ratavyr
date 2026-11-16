package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c := Load(func(string) string { return "" })
	if c.APIAddr != DefaultAPIAddr || c.EngineAddr != DefaultEngineAddr() || c.WebDir != "" || c.APIToken != "" {
		t.Fatalf("%+v", c)
	}
	if strings.Join(c.AllowedOrigins, ",") != DefaultAllowedOrigins {
		t.Fatalf("origins %v", c.AllowedOrigins)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLoopback(t *testing.T) {
	for addr, loopback := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:80": true, "[::1]:8080": true, "127.1.2.3:1": true,
		":8080": false, "0.0.0.0:8080": false, "192.168.1.2:8080": false, "[::]:8080": false, "bad": false,
	} {
		err := Config{APIAddr: addr}.Validate()
		if (err == nil) != loopback {
			t.Errorf("%s: %v", addr, err)
		}
		if err := (Config{APIAddr: addr, APIToken: "x"}).Validate(); err != nil {
			t.Errorf("%s with token: %v", addr, err)
		}
	}
}

func TestValidateTokensAndRates(t *testing.T) {
	ok := Config{APIAddr: "0.0.0.0:8080", APIToken: "admin-0123456789abcdef", OperatorToken: "op-0123456789abcdef"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Config{
		"duplicate":  {APIAddr: "0.0.0.0:8080", APIToken: "same-token-0123456789", ViewerToken: "same-token-0123456789"},
		"whitespace": {APIAddr: "0.0.0.0:8080", APIToken: " padded-token-0123456789"},
		"rate":       {APIAddr: "127.0.0.1:1", RateLimit: -1},
		"burst":      {APIAddr: "127.0.0.1:1", RateBurst: 0.5},
		// A viewer token alone still enables auth (non-loopback allowed).
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (Config{APIAddr: "0.0.0.0:8080", ViewerToken: "viewer-0123456789abcdef"}).Validate(); err != nil {
		t.Errorf("viewer-only auth: %v", err)
	}
}

func TestLoadParsesNewSettings(t *testing.T) {
	env := map[string]string{
		"MOTIONCORE_OPERATOR_TOKEN": "op", "MOTIONCORE_VIEWER_TOKEN": "v",
		"MOTIONCORE_ALLOWED_HOSTS": "robot.lan:*, 10.0.0.5:8080", "MOTIONCORE_AUDIT_LOG": "/var/log/audit.jsonl",
		"MOTIONCORE_RATE_LIMIT": "5", "MOTIONCORE_RATE_BURST": "nope",
	}
	c := Load(func(k string) string { return env[k] })
	if c.OperatorToken != "op" || c.ViewerToken != "v" || strings.Join(c.AllowedHosts, "|") != "robot.lan:*|10.0.0.5:8080" ||
		c.AuditLog != "/var/log/audit.jsonl" || c.RateLimit != 5 || c.RateBurst != -1 {
		t.Fatalf("%+v", c)
	}
	if c.Validate() == nil {
		t.Fatal("unparseable burst accepted")
	}
	if w := c.Warnings(); len(w) != 3 {
		t.Fatalf("warnings: %v", w) // two short tokens + no admin token
	}
}
