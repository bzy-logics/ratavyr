// Package enginetest provides an in-process fake MotionCore engine that
// implements the generated gRPC EngineService and FirmwareService servers.
package enginetest

import (
	"context"
	"net"
	"sync"
	"testing"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// HandlerFunc answers a unary RPC. method is the RPC name (e.g. "Jog",
// "StartBuild"); req is the request message. Return (nil, nil) for an empty
// response of the right type. Return an *Error to fail with a status code and
// the motioncore-error-code trailer.
type HandlerFunc func(ctx context.Context, method string, req proto.Message) (proto.Message, error)

// Error makes the fake fail an RPC with gRPC code Code and, if MCode is
// non-empty, trailer motioncore-error-code=MCode.
type Error struct {
	Code    codes.Code
	MCode   string
	Message string
}

func (e *Error) Error() string { return e.MCode + ": " + e.Message }

// Engine is a fake engine listening on 127.0.0.1:0.
type Engine struct {
	// Info is returned by GetInfo. Set before the client connects.
	Info *enginev1.EngineInfo
	// Initial is the first event of every StreamEvents call.
	Initial *enginev1.Telemetry

	handler HandlerFunc
	lis     net.Listener
	srv     *grpc.Server

	mu      sync.Mutex
	streams map[chan *enginev1.Event]chan error
	opened  int
	calls   []string
}

// New starts a fake engine; it is stopped at test cleanup. h may be nil.
func New(t testing.TB, h HandlerFunc) *Engine {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{
		Info: &enginev1.EngineInfo{
			ApiVersion: "0.3.0", EngineVersion: "0.2.0", McuProtocolVersion: 1, PluginApiVersion: "0",
		},
		Initial: &enginev1.Telemetry{Seq: 1, State: enginev1.RobotState_ROBOT_STATE_DISABLED, Mcu: &enginev1.McuTelemetry{}},
		handler: h,
		lis:     lis,
		srv:     grpc.NewServer(),
		streams: make(map[chan *enginev1.Event]chan error),
	}
	enginev1.RegisterEngineServiceServer(e.srv, &engineSrv{e: e})
	enginev1.RegisterFirmwareServiceServer(e.srv, &firmwareSrv{e: e})
	go e.srv.Serve(lis)
	t.Cleanup(e.Close)
	return e
}

// Addr returns the listen address (host:port).
func (e *Engine) Addr() string { return e.lis.Addr().String() }

// Close stops the server, closing all connections.
func (e *Engine) Close() { e.srv.Stop() }

// Streams returns how many StreamEvents calls were opened so far.
func (e *Engine) Streams() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opened
}

// Calls returns the unary RPC names called so far (excluding GetInfo).
func (e *Engine) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

// Emit sends ev to all open event streams.
func (e *Engine) Emit(ev *enginev1.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.streams {
		ch <- ev
	}
}

// EmitTelemetry sends a telemetry event.
func (e *Engine) EmitTelemetry(t *enginev1.Telemetry) {
	e.Emit(&enginev1.Event{Event: &enginev1.Event_Telemetry{Telemetry: t}})
}

// EmitLog sends a log event.
func (e *Engine) EmitLog(l *enginev1.LogEntry) {
	e.Emit(&enginev1.Event{Event: &enginev1.Event_Log{Log: l}})
}

// DropStreams ends all open event streams with UNAVAILABLE, as if the engine
// restarted. Clients reconnect afterwards.
func (e *Engine) DropStreams() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch, done := range e.streams {
		done <- status.Error(codes.Unavailable, "engine restarting")
		delete(e.streams, ch)
	}
}

// StreamEvents implements EngineService.
func (srv *engineSrv) StreamEvents(_ *enginev1.StreamEventsRequest, s grpc.ServerStreamingServer[enginev1.Event]) error {
	e := srv.e
	ch := make(chan *enginev1.Event, 1024)
	done := make(chan error, 1)
	e.mu.Lock()
	e.opened++
	e.streams[ch] = done
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.streams, ch)
		e.mu.Unlock()
	}()
	if err := s.Send(&enginev1.Event{Event: &enginev1.Event_Telemetry{Telemetry: e.Initial}}); err != nil {
		return err
	}
	for {
		select {
		case <-s.Context().Done():
			return s.Context().Err()
		case err := <-done:
			return err
		case ev := <-ch:
			if err := s.Send(ev); err != nil {
				return err
			}
		}
	}
}

// call dispatches a unary RPC to the handler. resp is the zero response.
func call[T proto.Message](e *Engine, ctx context.Context, method string, req proto.Message, resp T) (T, error) {
	e.mu.Lock()
	e.calls = append(e.calls, method)
	e.mu.Unlock()
	if e.handler == nil {
		return resp, nil
	}
	out, err := e.handler(ctx, method, req)
	if err != nil {
		var zero T
		if fe, ok := err.(*Error); ok {
			if fe.MCode != "" {
				grpc.SetTrailer(ctx, metadata.Pairs("motioncore-error-code", fe.MCode))
			}
			return zero, status.Error(fe.Code, fe.Message)
		}
		return zero, err
	}
	if out == nil {
		return resp, nil
	}
	return out.(T), nil
}

// GetInfo implements EngineService.
func (s *engineSrv) GetInfo(context.Context, *enginev1.GetInfoRequest) (*enginev1.EngineInfo, error) {
	s.e.mu.Lock()
	defer s.e.mu.Unlock()
	return s.e.Info, nil
}

// GetStatus implements EngineService.
func (s *engineSrv) GetStatus(ctx context.Context, r *enginev1.GetStatusRequest) (*enginev1.Telemetry, error) {
	return call(s.e, ctx, "GetStatus", r, &enginev1.Telemetry{})
}

// GetRobotConfig implements EngineService.
func (s *engineSrv) GetRobotConfig(ctx context.Context, r *enginev1.GetRobotConfigRequest) (*enginev1.RobotConfig, error) {
	return call(s.e, ctx, "GetRobotConfig", r, &enginev1.RobotConfig{})
}

// ListPlugins implements EngineService.
func (s *engineSrv) ListPlugins(ctx context.Context, r *enginev1.ListPluginsRequest) (*enginev1.ListPluginsResponse, error) {
	return call(s.e, ctx, "ListPlugins", r, &enginev1.ListPluginsResponse{})
}

// Enable implements EngineService.
func (s *engineSrv) Enable(ctx context.Context, r *enginev1.EnableRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "Enable", r, &enginev1.CommandResponse{})
}

// Disable implements EngineService.
func (s *engineSrv) Disable(ctx context.Context, r *enginev1.DisableRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "Disable", r, &enginev1.CommandResponse{})
}

// Jog implements EngineService.
func (s *engineSrv) Jog(ctx context.Context, r *enginev1.JogRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "Jog", r, &enginev1.CommandResponse{})
}

// MoveTo implements EngineService.
func (s *engineSrv) MoveTo(ctx context.Context, r *enginev1.MoveToRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "MoveTo", r, &enginev1.CommandResponse{})
}

// Home implements EngineService.
func (s *engineSrv) Home(ctx context.Context, r *enginev1.HomeRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "Home", r, &enginev1.CommandResponse{})
}

// MoveJ implements EngineService.
func (s *engineSrv) MoveJ(ctx context.Context, r *enginev1.MoveJRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "MoveJ", r, &enginev1.CommandResponse{})
}

// MoveJPath implements EngineService.
func (s *engineSrv) MoveJPath(ctx context.Context, r *enginev1.MoveJPathRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "MoveJPath", r, &enginev1.CommandResponse{})
}

// MoveL implements EngineService.
func (s *engineSrv) MoveL(ctx context.Context, r *enginev1.MoveLRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "MoveL", r, &enginev1.CommandResponse{})
}

// HomeAll implements EngineService.
func (s *engineSrv) HomeAll(ctx context.Context, r *enginev1.HomeAllRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "HomeAll", r, &enginev1.CommandResponse{})
}

// Stop implements EngineService.
func (s *engineSrv) Stop(ctx context.Context, r *enginev1.StopRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "Stop", r, &enginev1.CommandResponse{})
}

// EmergencyStop implements EngineService.
func (s *engineSrv) EmergencyStop(ctx context.Context, r *enginev1.EmergencyStopRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "EmergencyStop", r, &enginev1.CommandResponse{})
}

// ResetFault implements EngineService.
func (s *engineSrv) ResetFault(ctx context.Context, r *enginev1.ResetFaultRequest) (*enginev1.CommandResponse, error) {
	return call(s.e, ctx, "ResetFault", r, &enginev1.CommandResponse{})
}

// ListTargets implements FirmwareService.
func (s *firmwareSrv) ListTargets(ctx context.Context, r *enginev1.ListTargetsRequest) (*enginev1.ListTargetsResponse, error) {
	return call(s.e, ctx, "ListTargets", r, &enginev1.ListTargetsResponse{})
}

// StartBuild implements FirmwareService.
func (s *firmwareSrv) StartBuild(ctx context.Context, r *enginev1.StartBuildRequest) (*enginev1.FirmwareJob, error) {
	return call(s.e, ctx, "StartBuild", r, &enginev1.FirmwareJob{})
}

// StartFlash implements FirmwareService.
func (s *firmwareSrv) StartFlash(ctx context.Context, r *enginev1.StartFlashRequest) (*enginev1.FirmwareJob, error) {
	return call(s.e, ctx, "StartFlash", r, &enginev1.FirmwareJob{})
}

// GetJob implements FirmwareService.
func (s *firmwareSrv) GetJob(ctx context.Context, r *enginev1.GetJobRequest) (*enginev1.FirmwareJob, error) {
	return call(s.e, ctx, "GetJob", r, &enginev1.FirmwareJob{})
}

// ListJobs implements FirmwareService.
func (s *firmwareSrv) ListJobs(ctx context.Context, r *enginev1.ListJobsRequest) (*enginev1.ListJobsResponse, error) {
	return call(s.e, ctx, "ListJobs", r, &enginev1.ListJobsResponse{})
}

type engineSrv struct {
	enginev1.UnimplementedEngineServiceServer
	e *Engine
}

type firmwareSrv struct {
	enginev1.UnimplementedFirmwareServiceServer
	e *Engine
}
