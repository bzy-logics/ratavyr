// Package auth implements bearer-token authentication for the REST API.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/bzyfuzy/ratavyr/internal/api/rest"
)

// Public lists endpoints that never require a token.
var Public = map[string]bool{
	"/api/health": true,
	"/api/info":   true,
}

// WebSocketPath accepts the token as ?access_token= because browsers cannot
// set headers on WebSocket requests.
const WebSocketPath = "/api/ws"

// Middleware requires "Authorization: Bearer <token>" on /api/* except the
// Public endpoints. If token is empty, next is returned unchanged. Paths
// outside /api/ (the static web UI) are not protected.
func Middleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/api/") || Public[p] {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearer(r)
		if !ok && p == WebSocketPath {
			got, ok = r.URL.Query().Get("access_token"), true
		}
		// Compare fixed-size digests so neither content nor length leaks.
		sum := sha256.Sum256([]byte(got))
		if !ok || got == "" || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="motioncore"`)
			rest.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, tok, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(tok), true
}
