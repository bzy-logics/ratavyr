// Package config loads the server configuration from the environment.
package config

import (
	"fmt"
	"net"
	"runtime"
	"strings"
)

// Config is the server configuration.
type Config struct {
	// APIAddr is the HTTP listen address (MOTIONCORE_API_ADDR).
	APIAddr string
	// EngineAddr is the engine gRPC target (MOTIONCORE_ENGINE_ADDR), either a
	// gRPC target URI such as unix:///run/motioncore/engine.sock or a plain
	// host:port.
	EngineAddr string
	// WebDir is an optional directory with the built web UI (MOTIONCORE_WEB_DIR).
	WebDir string
	// APIToken, when set, is required as a bearer token (MOTIONCORE_API_TOKEN).
	APIToken string
	// AllowedOrigins are extra WebSocket origin host patterns
	// (MOTIONCORE_ALLOWED_ORIGINS, comma separated).
	AllowedOrigins []string
}

// Defaults.
const (
	DefaultAPIAddr        = "127.0.0.1:8080"
	DefaultAllowedOrigins = "localhost:5173,127.0.0.1:5173"
)

// DefaultEngineAddr is the engine address used when MOTIONCORE_ENGINE_ADDR is
// unset: the engine's Unix socket on Linux, loopback TCP elsewhere.
func DefaultEngineAddr() string {
	if runtime.GOOS == "linux" {
		return "unix:///run/motioncore/engine.sock"
	}
	return "127.0.0.1:7600"
}

// Load reads the configuration using getenv (typically os.Getenv).
func Load(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	var origins []string
	for _, o := range strings.Split(get("MOTIONCORE_ALLOWED_ORIGINS", DefaultAllowedOrigins), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return Config{
		APIAddr:        get("MOTIONCORE_API_ADDR", DefaultAPIAddr),
		EngineAddr:     get("MOTIONCORE_ENGINE_ADDR", DefaultEngineAddr()),
		WebDir:         get("MOTIONCORE_WEB_DIR", ""),
		APIToken:       getenv("MOTIONCORE_API_TOKEN"),
		AllowedOrigins: origins,
	}
}

// Validate rejects unsafe configurations: without an API token the server
// must only listen on a loopback address.
func (c Config) Validate() error {
	if c.APIToken != "" {
		return nil
	}
	if !IsLoopback(c.APIAddr) {
		return fmt.Errorf("refusing to listen on non-loopback address %q without authentication: "+
			"set MOTIONCORE_API_TOKEN or use a loopback MOTIONCORE_API_ADDR such as %s", c.APIAddr, DefaultAPIAddr)
	}
	return nil
}

// IsLoopback reports whether the listen address addr (host:port) only binds
// loopback interfaces. An empty host means all interfaces.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
