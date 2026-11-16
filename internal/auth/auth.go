// Package auth implements authentication and authorization for the REST and
// WebSocket API.
//
// Roles, from least to most privileged:
//
//   - viewer:   read everything; may also send stop and estop (anyone who can
//     see the robot may stop it)
//   - operator: viewer + motion and drive commands, firmware builds
//   - admin:    operator + firmware flashing
//
// Tokens are configured per role (see package config). When no token is
// configured at all, authentication is off and every request is admin; the
// server then only listens on loopback (config.Validate) and package guard
// blocks cross-site and DNS-rebinding requests.
//
// Browsers cannot set headers on WebSocket requests, so /api/ws accepts a
// short-lived single-use ticket (POST /api/ws-tickets) as ?ticket=. The
// legacy ?access_token=<token> still works but is deprecated because it puts
// the long-lived token into URLs and logs.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bzyfuzy/ratavyr/internal/api/rest"
)

// Role is an authorization level.
type Role int

// Roles in increasing privilege.
const (
	None Role = iota
	Viewer
	Operator
	Admin
)

func (r Role) String() string {
	switch r {
	case Viewer:
		return "viewer"
	case Operator:
		return "operator"
	case Admin:
		return "admin"
	default:
		return "none"
	}
}

// Public lists endpoints that never require a token.
var Public = map[string]bool{
	"/api/health": true,
	"/api/info":   true,
}

// WebSocketPath is the WebSocket endpoint.
const WebSocketPath = "/api/ws"

// TicketTTL is how long a WebSocket ticket stays valid.
const TicketTTL = 30 * time.Second

// Failed-authentication limits per client IP.
const (
	MaxAuthFailures   = 10
	AuthFailureWindow = time.Minute
)

type ctxKey struct{}

// RoleFrom returns the role of an authenticated request (None if absent).
func RoleFrom(ctx context.Context) Role {
	r, _ := ctx.Value(ctxKey{}).(Role)
	return r
}

// WithRole returns ctx carrying role (for tests and internal use).
func WithRole(ctx context.Context, r Role) context.Context {
	return context.WithValue(ctx, ctxKey{}, r)
}

// Tokens maps each role to its token; empty strings are unused roles.
type Tokens struct {
	Admin, Operator, Viewer string
}

// Enabled reports whether any token is configured.
func (t Tokens) Enabled() bool { return t.Admin != "" || t.Operator != "" || t.Viewer != "" }

type ticket struct {
	role    Role
	expires time.Time
}

// Authenticator validates tokens and tickets and tracks failures.
type Authenticator struct {
	enabled bool
	digests []struct {
		sum  [32]byte
		role Role
	}
	now func() time.Time

	mu       sync.Mutex
	tickets  map[string]ticket
	failures map[string][]time.Time
	// OnDeprecatedQueryToken is called when a client authenticates the
	// WebSocket with ?access_token= (for logging).
	OnDeprecatedQueryToken func(r *http.Request)
	// OnFailure is called on every rejected authentication (for auditing).
	OnFailure func(r *http.Request, reason string)
}

// New creates an Authenticator for the configured tokens.
func New(t Tokens) *Authenticator {
	a := &Authenticator{
		enabled:  t.Enabled(),
		now:      time.Now,
		tickets:  make(map[string]ticket),
		failures: make(map[string][]time.Time),
	}
	for _, e := range []struct {
		tok  string
		role Role
	}{{t.Admin, Admin}, {t.Operator, Operator}, {t.Viewer, Viewer}} {
		if e.tok != "" {
			a.digests = append(a.digests, struct {
				sum  [32]byte
				role Role
			}{sha256.Sum256([]byte(e.tok)), e.role})
		}
	}
	return a
}

// Enabled reports whether authentication is required.
func (a *Authenticator) Enabled() bool { return a.enabled }

// roleForToken compares against every configured token in constant time
// (fixed-size digests, no early exit) so neither content nor which role
// matched leaks through timing.
func (a *Authenticator) roleForToken(tok string) Role {
	if tok == "" {
		return None
	}
	sum := sha256.Sum256([]byte(tok))
	role := None
	for _, d := range a.digests {
		if subtle.ConstantTimeCompare(sum[:], d.sum[:]) == 1 {
			role = d.role
		}
	}
	return role
}

// IssueTicket returns a single-use WebSocket ticket for role.
func (a *Authenticator) IssueTicket(role Role) (string, time.Duration, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", 0, err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, v := range a.tickets {
		if now.After(v.expires) {
			delete(a.tickets, k)
		}
	}
	a.tickets[t] = ticket{role: role, expires: now.Add(TicketTTL)}
	return t, TicketTTL, nil
}

// redeemTicket consumes a ticket; it is valid once, before it expires.
func (a *Authenticator) redeemTicket(t string) Role {
	if t == "" {
		return None
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tk, ok := a.tickets[t]
	delete(a.tickets, t)
	if !ok || a.now().After(tk.expires) {
		return None
	}
	return tk.role
}

// ClientIP is the request's remote IP (no proxy headers are trusted).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// blocked reports whether ip exceeded the failure budget; it also prunes.
func (a *Authenticator) blocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := a.now().Add(-AuthFailureWindow)
	f := a.failures[ip]
	i := 0
	for i < len(f) && f[i].Before(cutoff) {
		i++
	}
	f = f[i:]
	if len(f) == 0 {
		delete(a.failures, ip)
	} else {
		a.failures[ip] = f
	}
	return len(f) >= MaxAuthFailures
}

func (a *Authenticator) fail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[ip] = append(a.failures[ip], a.now())
}

// Middleware authenticates /api/* requests (except Public) and stores the
// role in the request context. Paths outside /api/ (the static web UI) are
// not protected. Without configured tokens every request is admin.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !a.enabled {
			next.ServeHTTP(w, r.WithContext(WithRole(r.Context(), Admin)))
			return
		}
		if !strings.HasPrefix(p, "/api/") || Public[p] {
			next.ServeHTTP(w, r)
			return
		}
		ip := ClientIP(r)
		if a.blocked(ip) {
			w.Header().Set("Retry-After", "60")
			rest.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many failed authentication attempts; retry later")
			return
		}
		role := None
		if tok, ok := bearer(r); ok {
			role = a.roleForToken(tok)
		} else if p == WebSocketPath {
			q := r.URL.Query()
			if t := q.Get("ticket"); t != "" {
				role = a.redeemTicket(t)
			} else if t := q.Get("access_token"); t != "" {
				role = a.roleForToken(t)
				if role != None && a.OnDeprecatedQueryToken != nil {
					a.OnDeprecatedQueryToken(r)
				}
			}
		}
		if role == None {
			a.fail(ip)
			if a.OnFailure != nil {
				a.OnFailure(r, "missing or invalid credentials")
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="motioncore"`)
			rest.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API token")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithRole(r.Context(), role)))
	})
}

// Require wraps h so that only requests with at least role min pass; others
// get 403 forbidden.
func Require(min Role, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := RoleFrom(r.Context()); got < min {
			rest.WriteError(w, http.StatusForbidden, "forbidden",
				"this action requires the "+min.String()+" role (you are "+got.String()+")")
			return
		}
		h(w, r)
	}
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, tok, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(tok), true
}
