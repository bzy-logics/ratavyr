package engine_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzy-logics/ratavyr/internal/engine"
	"github.com/bzy-logics/ratavyr/internal/engine/enginetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func startClient(t *testing.T, opts engine.Options) *engine.Client {
	t.Helper()
	opts.MinBackoff = 10 * time.Millisecond
	opts.MaxBackoff = 50 * time.Millisecond
	c, err := engine.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func nextEvent(t *testing.T, ch <-chan engine.Event) engine.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("subscriber channel closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return engine.Event{}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestTarget(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7600":                      "passthrough:///127.0.0.1:7600",
		"localhost:7600":                      "passthrough:///localhost:7600",
		"unix:///run/motioncore/engine.sock":  "unix:///run/motioncore/engine.sock",
		"dns:///engine.local:7600":            "dns:///engine.local:7600",
		"passthrough:///10.0.0.1:7600":        "passthrough:///10.0.0.1:7600",
		"[::1]:7600":                          "passthrough:///[::1]:7600",
		"unix-abstract:motioncore-engine-dev": "unix-abstract:motioncore-engine-dev",
	} {
		if got := engine.Target(in); got != want {
			t.Errorf("Target(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckAPIVersion(t *testing.T) {
	for v, ok := range map[string]bool{"0.3.0": true, "0.3.7": true, "0.3.99": true, "0.2.0": false, "0.4.0": false, "1.2.0": false, "0.3": true, "0": false, "": false, "x": false} {
		if err := engine.CheckAPIVersion(v); (err == nil) != ok {
			t.Errorf("CheckAPIVersion(%q) = %v", v, err)
		}
	}
}

func TestCallAndTrailerErrors(t *testing.T) {
	fake := enginetest.New(t, func(_ context.Context, method string, req proto.Message) (proto.Message, error) {
		switch r := req.(type) {
		case *enginev1.HomeRequest:
			return nil, &enginetest.Error{Code: codes.FailedPrecondition, MCode: r.Axis, Message: "msg " + r.Axis}
		case *enginev1.JogRequest:
			return nil, &enginetest.Error{Code: codes.Unavailable, Message: "no trailer"}
		}
		return &enginev1.Telemetry{Seq: 42}, nil
	})
	c := startClient(t, engine.Options{Target: fake.Addr()})
	waitFor(t, "connect", c.Connected)

	tel, err := engine.Call(context.Background(), c, c.Engine.GetStatus, &enginev1.GetStatusRequest{})
	if err != nil || tel.GetSeq() != 42 {
		t.Fatalf("GetStatus: %v %v", tel, err)
	}
	if got := c.Info().GetApiVersion(); got != "0.3.0" {
		t.Fatalf("Info: %q", got)
	}

	_, err = engine.Call(context.Background(), c, c.Engine.Home, &enginev1.HomeRequest{Axis: "not_homed"})
	var ee *engine.Error
	if !errors.As(err, &ee) || ee.Code != "not_homed" || ee.Message != "msg not_homed" || ee.Status != codes.FailedPrecondition {
		t.Fatalf("Home: %#v", err)
	}
	// UNAVAILABLE without trailer: the engine (or the link to it) is down.
	_, err = engine.Call(context.Background(), c, c.Engine.Jog, &enginev1.JogRequest{})
	if !errors.Is(err, engine.ErrUnavailable) {
		t.Fatalf("Jog: %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	fake := enginetest.New(t, func(ctx context.Context, _ string, _ proto.Message) (proto.Message, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	c := startClient(t, engine.Options{Target: fake.Addr(), RequestTimeout: 100 * time.Millisecond})
	waitFor(t, "connect", c.Connected)
	if _, err := engine.Call(context.Background(), c, c.Engine.Stop, &enginev1.StopRequest{}); !errors.Is(err, engine.ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

func TestUnavailableAndReconnect(t *testing.T) {
	c := startClient(t, engine.Options{Target: freeAddr(t)})
	time.Sleep(50 * time.Millisecond)
	if c.Connected() {
		t.Fatal("connected to nothing")
	}
	if _, err := engine.Call(context.Background(), c, c.Engine.GetStatus, &enginev1.GetStatusRequest{}); !errors.Is(err, engine.ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}

	fake := enginetest.New(t, nil)
	c2 := startClient(t, engine.Options{Target: fake.Addr()})
	waitFor(t, "connect", c2.Connected)
	ch, cancel := c2.Subscribe()
	defer cancel()
	if ev := nextEvent(t, ch); ev.Type != engine.EventEngine || !ev.Connected {
		t.Fatalf("first event %+v", ev)
	}
	if ev := nextEvent(t, ch); ev.Type != engine.EventTelemetry || ev.Telemetry.GetSeq() != 1 {
		t.Fatalf("cached telemetry %+v", ev)
	}
	fake.DropStreams()
	if ev := nextEvent(t, ch); ev.Type != engine.EventEngine || ev.Connected {
		t.Fatalf("want disconnect, got %+v", ev)
	}
	if ev := nextEvent(t, ch); ev.Type != engine.EventEngine || !ev.Connected {
		t.Fatalf("want reconnect, got %+v", ev)
	}
	if fake.Streams() != 2 {
		t.Fatalf("streams opened: %d", fake.Streams())
	}
}

func TestIncompatibleAPIVersionRefused(t *testing.T) {
	fake := enginetest.New(t, nil)
	fake.Info.ApiVersion = "0.2.0"
	c := startClient(t, engine.Options{Target: fake.Addr()})
	var err error
	waitFor(t, "refusal", func() bool {
		_, err = engine.Call(context.Background(), c, c.Engine.GetStatus, &enginev1.GetStatusRequest{})
		return err != nil && strings.Contains(err.Error(), "incompatible")
	})
	if c.Connected() || c.Info() != nil {
		t.Fatal("connected to an incompatible engine")
	}
	if fake.Streams() != 0 {
		t.Fatal("subscribed to an incompatible engine")
	}
	if !errors.Is(err, engine.ErrUnavailable) || !strings.Contains(err.Error(), `"0.2.0"`) {
		t.Fatalf("got %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("engine was called: %v", fake.Calls())
	}
}

func TestSlowSubscriberDropped(t *testing.T) {
	fake := enginetest.New(t, nil)
	c := startClient(t, engine.Options{Target: fake.Addr(), SubscriberBuffer: 4})
	waitFor(t, "connect", c.Connected)
	slow, _ := c.Subscribe()
	fast, cancel := c.Subscribe()
	defer cancel()

	nextEvent(t, fast) // engine status
	nextEvent(t, fast) // cached telemetry
	for i := range 10 {
		fake.EmitLog(&enginev1.LogEntry{Message: fmt.Sprint(i)})
		if ev := nextEvent(t, fast); ev.Type != engine.EventLog {
			t.Fatalf("got %v", ev.Type)
		}
	}
	n := 0
	for range slow {
		n++
	}
	if n != 4 {
		t.Fatalf("slow subscriber got %d buffered events before close, want 4", n)
	}
}
