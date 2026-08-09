package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/bzyfuzy/ratavyr/internal/engine"
)

// MaxBodyBytes limits request bodies.
const MaxBodyBytes = 64 << 10

// ErrorBody is the JSON error envelope.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the content of ErrorBody.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes an error envelope with the given status.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{ErrorDetail{Code: code, Message: message}})
}

// BadRequest writes a 400 bad_request error.
func BadRequest(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, "bad_request", message)
}

// StatusForCode maps a MotionCore error code to its HTTP status.
func StatusForCode(code string) int {
	switch code {
	case "bad_request", "validation_failed", "not_homed":
		return http.StatusBadRequest
	case "unauthorized":
		return http.StatusUnauthorized
	case "not_found", "unknown_axis", "unknown_command":
		return http.StatusNotFound
	case "invalid_state":
		return http.StatusConflict
	case "mcu_error":
		return http.StatusBadGateway
	case "engine_unavailable":
		return http.StatusServiceUnavailable
	case "engine_timeout":
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// WriteEngineError maps an error from engine.Call to an HTTP error response.
func WriteEngineError(w http.ResponseWriter, log *slog.Logger, op string, err error) {
	var ee *engine.Error
	switch {
	case errors.Is(err, engine.ErrUnavailable):
		WriteError(w, http.StatusServiceUnavailable, "engine_unavailable", err.Error())
	case errors.Is(err, engine.ErrTimeout):
		WriteError(w, http.StatusGatewayTimeout, "engine_timeout", "engine did not respond in time")
	case errors.As(err, &ee):
		WriteError(w, StatusForCode(ee.Code), ee.Code, ee.Message)
	case errors.Is(err, context.Canceled):
		// Client went away; nothing useful to write.
	default:
		if log == nil {
			log = slog.Default()
		}
		log.Error("engine call failed", "op", op, "err", err)
		WriteError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// ReadObject reads the request body, which must be empty or a JSON object.
// It returns "{}" for an empty body. On failure it writes a 400 response and
// returns false.
func ReadObject(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		BadRequest(w, "cannot read body: "+err.Error())
		return nil, false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return json.RawMessage("{}"), true
	}
	var obj map[string]json.RawMessage
	if body[0] != '{' || json.Unmarshal(body, &obj) != nil {
		BadRequest(w, "body must be a JSON object")
		return nil, false
	}
	return body, true
}

// Decode decodes a JSON object into dst, rejecting unknown fields.
func Decode(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid parameters: %v", err)
	}
	return nil
}

// DecodeObject reads an empty-or-object body into dst with Decode. On failure
// it writes a 400 response and returns false.
func DecodeObject(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, ok := ReadObject(w, r)
	if !ok {
		return false
	}
	if err := Decode(body, dst); err != nil {
		BadRequest(w, err.Error())
		return false
	}
	return true
}
