package engine

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ErrorCodeKey is the trailing metadata key carrying the MotionCore error code.
const ErrorCodeKey = "motioncore-error-code"

var (
	// ErrUnavailable is returned when there is no usable engine connection.
	ErrUnavailable = errors.New("engine unavailable")
	// ErrTimeout is returned when the engine does not answer in time.
	ErrTimeout = errors.New("engine request timed out")
)

// Error is an error reported by the engine.
type Error struct {
	// Code is the MotionCore error code (e.g. "not_homed"), taken from the
	// motioncore-error-code trailer or derived from the gRPC status code.
	Code    string
	Message string
	Status  codes.Code
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Call invokes a unary engine RPC with the client's request timeout. It
// returns ErrUnavailable without calling the engine while the client is not
// connected (including after a refused handshake), ErrTimeout when the
// deadline expires, ctx.Err() when the caller gave up, and *Error for errors
// reported by the engine.
//
//	tel, err := engine.Call(ctx, c, c.Engine.GetStatus, &enginev1.GetStatusRequest{})
func Call[Req, Resp any](ctx context.Context, c *Client, rpc func(context.Context, Req, ...grpc.CallOption) (Resp, error), req Req) (Resp, error) {
	var zero Resp
	if !c.Connected() {
		return zero, c.unavailable()
	}
	cctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	var trailer metadata.MD
	resp, err := rpc(cctx, req, grpc.Trailer(&trailer))
	if err != nil {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, translate(err, trailer)
	}
	return resp, nil
}

// translate converts a gRPC error plus trailer into the package's errors.
func translate(err error, trailer metadata.MD) error {
	st, ok := status.FromError(err)
	if !ok {
		return &Error{Code: "internal", Message: err.Error(), Status: codes.Unknown}
	}
	if vals := trailer.Get(ErrorCodeKey); len(vals) > 0 && vals[0] != "" {
		// The engine's own code wins, e.g. UNAVAILABLE + mcu_error is an MCU
		// problem, not an unreachable engine.
		return &Error{Code: vals[0], Message: st.Message(), Status: st.Code()}
	}
	switch st.Code() {
	case codes.DeadlineExceeded:
		return ErrTimeout
	case codes.Unavailable:
		return fmt.Errorf("%w: %s", ErrUnavailable, st.Message())
	}
	return &Error{Code: codeFor(st.Code()), Message: st.Message(), Status: st.Code()}
}

// codeFor derives a MotionCore code for statuses without the trailer.
func codeFor(c codes.Code) string {
	switch c {
	case codes.InvalidArgument, codes.OutOfRange:
		return "bad_request"
	case codes.NotFound:
		return "not_found"
	case codes.FailedPrecondition:
		return "invalid_state"
	default:
		return "internal"
	}
}
