// Package guard protects the API against cross-site requests, DNS rebinding
// and request floods.
//
//   - Cross-site requests: state-changing /api/ requests (POST) may not use
//     the content types a browser can send cross-site without a CORS
//     preflight (text/plain, form-urlencoded, multipart); JSON requests
//     from another origin need a preflight, which this server never
//     approves. When the browser sends an Origin, it must be the server's
//     own origin or an allowed pattern.
//   - DNS rebinding: while authentication is off, the Host header must be a
//     loopback name or an allowed host; a rebinding page reaches 127.0.0.1
//     with its own host name in Host.
//   - Floods: POST requests are rate limited per client IP. Stop and
//     emergency-stop commands are never limited.
package guard

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/bzyfuzy/ratavyr/internal/api/rest"
	"github.com/bzyfuzy/ratavyr/internal/auth"
)

// Options configures the guard.
type Options struct {
	// AuthEnabled selects whether the Host check applies (only without auth).
	AuthEnabled bool
	// OriginPatterns are allowed cross-origin hosts (e.g. "localhost:5173"),
	// matched with path.Match against the Origin's host[:port].
	OriginPatterns []string
	// AllowedHosts are extra Host header values accepted without auth.
	AllowedHosts []string
	// Rate and Burst limit POST requests per client IP (per second).
	Rate  float64
	Burst float64
	// Now is the clock (tests).
	Now func() time.Time
}

// Defaults for the POST rate limit.
const (
	DefaultRate  = 20
	DefaultBurst = 40
)

// Unlimited lists POST paths that are never rate limited.
var Unlimited = map[string]bool{
	"/api/commands/estop": true,
	"/api/commands/stop":  true,
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Guard is the middleware state.
type Guard struct {
	o       Options
	mu      sync.Mutex
	buckets map[string]*bucket
}

// New creates a Guard.
func New(o Options) *Guard {
	if o.Rate <= 0 {
		o.Rate = DefaultRate
	}
	if o.Burst < 1 {
		o.Burst = DefaultBurst
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Guard{o: o, buckets: make(map[string]*bucket)}
}

// Middleware applies the checks to /api/ requests.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		if !g.o.AuthEnabled && !g.hostAllowed(r.Host) {
			rest.WriteError(w, http.StatusForbidden, "forbidden",
				"host "+r.Host+" is not allowed without authentication (DNS rebinding protection); set MOTIONCORE_ALLOWED_HOSTS or an API token")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if o := r.Header.Get("Origin"); o != "" && !g.originAllowed(o, r.Host) {
				rest.WriteError(w, http.StatusForbidden, "forbidden", "cross-origin request from "+o+" rejected")
				return
			}
			if ct := r.Header.Get("Content-Type"); ct != "" {
				if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
					rest.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", `request bodies must be "Content-Type: application/json"`)
					return
				}
			}
			if !Unlimited[r.URL.Path] && !g.allow(auth.ClientIP(r)) {
				w.Header().Set("Retry-After", "1")
				rest.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; slow down")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether host (Host header, maybe with port) names
// the local machine.
func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (g *Guard) hostAllowed(host string) bool {
	if isLoopbackHost(host) {
		return true
	}
	for _, p := range g.o.AllowedHosts {
		if ok, _ := path.Match(strings.ToLower(p), strings.ToLower(host)); ok {
			return true
		}
	}
	return false
}

func (g *Guard) originAllowed(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	for _, p := range g.o.OriginPatterns {
		if ok, _ := path.Match(strings.ToLower(p), strings.ToLower(u.Host)); ok {
			return true
		}
	}
	return false
}

// allow takes one token from ip's bucket.
func (g *Guard) allow(ip string) bool {
	now := g.o.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.buckets[ip]
	if b == nil {
		if len(g.buckets) > 10_000 {
			// Drop idle buckets so the map cannot grow without bound.
			for k, v := range g.buckets {
				if now.Sub(v.last) > time.Minute {
					delete(g.buckets, k)
				}
			}
		}
		b = &bucket{tokens: g.o.Burst, last: now}
		g.buckets[ip] = b
	}
	b.tokens = min(g.o.Burst, b.tokens+now.Sub(b.last).Seconds()*g.o.Rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
