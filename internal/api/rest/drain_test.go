package rest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bzy-logics/ratavyr/internal/api/rest"
)

// countingReader records how many bytes were read from it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func reject(w http.ResponseWriter, _ *http.Request) {
	rest.WriteError(w, http.StatusNotFound, "unknown_command", "unknown command: nope")
}

func post(body *countingReader, size int64) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/commands/nope", body)
	r.ContentLength = size
	return r
}

func TestDrainBodyReadsUnreadBodyBeforeResponding(t *testing.T) {
	body := &countingReader{r: strings.NewReader(strings.Repeat("x", 1000))}
	w := httptest.NewRecorder()
	rest.DrainBody(http.HandlerFunc(reject)).ServeHTTP(w, post(body, 1000))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if body.n != 1000 {
		t.Errorf("read %d of 1000 body bytes", body.n)
	}
}

func TestDrainBodyIsBounded(t *testing.T) {
	size := int64(rest.MaxBodyBytes * 2)
	body := &countingReader{r: strings.NewReader(strings.Repeat("x", int(size)))}
	rest.DrainBody(http.HandlerFunc(reject)).ServeHTTP(httptest.NewRecorder(), post(body, size))
	if body.n != rest.MaxBodyBytes {
		t.Errorf("read %d bytes, want at most MaxBodyBytes (%d)", body.n, rest.MaxBodyBytes)
	}
}

func TestDrainBodyKeepsWhatTheHandlerRead(t *testing.T) {
	body := &countingReader{r: strings.NewReader(`{"axis":"j1"}`)}
	var got string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		rest.WriteJSON(w, http.StatusOK, struct{}{})
	})
	rest.DrainBody(h).ServeHTTP(httptest.NewRecorder(), post(body, 13))
	if got != `{"axis":"j1"}` {
		t.Errorf("handler read %q", got)
	}
}

func TestDrainBodyPassesBodilessRequestsThrough(t *testing.T) {
	w := httptest.NewRecorder()
	h := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		// WebSocket upgrades need the original writer (http.Hijacker).
		if rw != http.ResponseWriter(w) {
			t.Errorf("writer was wrapped for a request without a body")
		}
	})
	rest.DrainBody(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/ws", nil))
}
