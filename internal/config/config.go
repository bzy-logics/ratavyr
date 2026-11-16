// Package config loads the server configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
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
	// APIToken is the admin token (MOTIONCORE_API_TOKEN). Kept under its
	// original name: existing deployments keep full access with it.
	APIToken string
	// OperatorToken (MOTIONCORE_OPERATOR_TOKEN) may command motion but not flash.
	OperatorToken string
	// ViewerToken (MOTIONCORE_VIEWER_TOKEN) is read-only plus stop/estop.
	ViewerToken string
	// AllowedOrigins are extra cross-origin host patterns for the WebSocket
	// and for state-changing requests (MOTIONCORE_ALLOWED_ORIGINS).
	AllowedOrigins []string
	// AllowedHosts are extra Host header patterns accepted while
	// authentication is off (MOTIONCORE_ALLOWED_HOSTS).
	AllowedHosts []string
	// AuditLog is a file that receives JSON audit records
	// (MOTIONCORE_AUDIT_LOG); empty = audit records go to the main log.
	AuditLog string
	// RateLimit / RateBurst limit POST requests per client IP
	// (MOTIONCORE_RATE_LIMIT, MOTIONCORE_RATE_BURST); stop/estop are exempt.
	RateLimit float64
	RateBurst float64
}

// Defaults.
const (
	DefaultAPIAddr        = "127.0.0.1:8080"
	DefaultAllowedOrigins = "localhost:5173,127.0.0.1:5173"
	DefaultRateLimit      = 20
	DefaultRateBurst      = 40
	// MinTokenLength is the shortest token accepted without a warning.
	MinTokenLength = 16
)

// DefaultEngineAddr is the engine address used when MOTIONCORE_ENGINE_ADDR is
// unset: the engine's Unix socket on Linux, loopback TCP elsewhere.
func DefaultEngineAddr() string {
	if runtime.GOOS == "linux" {
		return "unix:///run/motioncore/engine.sock"
	}
	return "127.0.0.1:7600"
}

func list(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// Load reads the configuration using getenv (typically os.Getenv).
// Malformed numbers are reported by Validate (as zero values below the
// minimum).
func Load(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	num := func(key string, def float64) float64 {
		v := get(key, "")
		if v == "" {
			return def
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return -1
		}
		return f
	}
	return Config{
		APIAddr:        get("MOTIONCORE_API_ADDR", DefaultAPIAddr),
		EngineAddr:     get("MOTIONCORE_ENGINE_ADDR", DefaultEngineAddr()),
		WebDir:         get("MOTIONCORE_WEB_DIR", ""),
		APIToken:       getenv("MOTIONCORE_API_TOKEN"),
		OperatorToken:  getenv("MOTIONCORE_OPERATOR_TOKEN"),
		ViewerToken:    getenv("MOTIONCORE_VIEWER_TOKEN"),
		AllowedOrigins: list(get("MOTIONCORE_ALLOWED_ORIGINS", DefaultAllowedOrigins)),
		AllowedHosts:   list(get("MOTIONCORE_ALLOWED_HOSTS", "")),
		AuditLog:       get("MOTIONCORE_AUDIT_LOG", ""),
		RateLimit:      num("MOTIONCORE_RATE_LIMIT", DefaultRateLimit),
		RateBurst:      num("MOTIONCORE_RATE_BURST", DefaultRateBurst),
	}
}

// AuthEnabled reports whether any API token is configured.
func (c Config) AuthEnabled() bool {
	return c.APIToken != "" || c.OperatorToken != "" || c.ViewerToken != ""
}

// Validate rejects unsafe or inconsistent configurations: without any token
// the server must only listen on loopback; tokens must be distinct and free
// of surrounding whitespace; rate settings must be valid (zero = default).
func (c Config) Validate() error {
	var errs []error
	if !c.AuthEnabled() && !IsLoopback(c.APIAddr) {
		errs = append(errs, fmt.Errorf("refusing to listen on non-loopback address %q without authentication: "+
			"set MOTIONCORE_API_TOKEN or use a loopback MOTIONCORE_API_ADDR such as %s", c.APIAddr, DefaultAPIAddr))
	}
	seen := map[string]string{}
	for _, t := range []struct{ name, val string }{
		{"MOTIONCORE_API_TOKEN", c.APIToken},
		{"MOTIONCORE_OPERATOR_TOKEN", c.OperatorToken},
		{"MOTIONCORE_VIEWER_TOKEN", c.ViewerToken},
	} {
		if t.val == "" {
			continue
		}
		if strings.TrimSpace(t.val) != t.val {
			errs = append(errs, fmt.Errorf("%s has leading or trailing whitespace", t.name))
		}
		if other, dup := seen[t.val]; dup {
			errs = append(errs, fmt.Errorf("%s and %s must differ", other, t.name))
		}
		seen[t.val] = t.name
	}
	// Zero selects the default; Load reports unparseable values as -1.
	if c.RateLimit < 0 {
		errs = append(errs, errors.New("MOTIONCORE_RATE_LIMIT must be a positive number"))
	}
	if c.RateBurst < 0 || (c.RateBurst > 0 && c.RateBurst < 1) {
		errs = append(errs, errors.New("MOTIONCORE_RATE_BURST must be at least 1"))
	}
	return errors.Join(errs...)
}

// Warnings lists weak but accepted settings.
func (c Config) Warnings() []string {
	var w []string
	for _, t := range []struct{ name, val string }{
		{"MOTIONCORE_API_TOKEN", c.APIToken},
		{"MOTIONCORE_OPERATOR_TOKEN", c.OperatorToken},
		{"MOTIONCORE_VIEWER_TOKEN", c.ViewerToken},
	} {
		if t.val != "" && len(t.val) < MinTokenLength {
			w = append(w, fmt.Sprintf("%s is shorter than %d characters; use a long random token", t.name, MinTokenLength))
		}
	}
	if c.AuthEnabled() && c.APIToken == "" {
		w = append(w, "no admin token (MOTIONCORE_API_TOKEN): firmware flashing is unavailable")
	}
	return w
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
