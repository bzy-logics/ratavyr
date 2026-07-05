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
