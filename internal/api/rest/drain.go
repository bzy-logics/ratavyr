package rest

import (
	"io"
	"net/http"
)

// DrainBody reads what is left of the request body (at most MaxBodyBytes)
// before the response starts.
//
// Many handlers and middlewares reject a request without reading its body:
// unknown command, role too low, unauthorized, rate limited, no such
// endpoint. net/http normally discards such a body itself, but not when the
// client sent "Connection: close" (Python's urllib always does). The server
// then closes the socket with unread data in its receive buffer, the OS
// answers with a TCP reset, and the client may never see the JSON error.
//
// Requests without a body (including WebSocket upgrades) are passed through
// untouched. A body larger than MaxBodyBytes is not drained further; the
// connection is closed as before.
func DrainBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&drainWriter{ResponseWriter: w, body: r.Body}, r)
	})
}

type drainWriter struct {
	http.ResponseWriter
	body    io.Reader
	drained bool
}

func (d *drainWriter) drain() {
	if !d.drained {
		d.drained = true
		_, _ = io.Copy(io.Discard, io.LimitReader(d.body, MaxBodyBytes))
	}
}

func (d *drainWriter) WriteHeader(code int) {
	d.drain()
	d.ResponseWriter.WriteHeader(code)
}

func (d *drainWriter) Write(b []byte) (int, error) {
	d.drain()
	return d.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (d *drainWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
