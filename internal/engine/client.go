// Package engine is the gRPC client for the MotionCore engine
// (motioncore.engine.v1, generated bindings in gen/).
//
// The Client keeps one gRPC channel to the engine. Its Run loop performs the
// GetInfo handshake (refusing engines whose api_version has another 0.MINOR),
// subscribes to StreamEvents, caches the latest telemetry and fans events out
// to local subscribers, reconnecting with exponential backoff. It never
// synthesises robot state; it only relays what the engine reports.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

// SupportedAPIMajor and SupportedAPIMinor identify the engine-api contract
// this server implements (contract.lock). While the major is 0, an engine is
// compatible only with the same 0.MINOR: minor releases may break clients,
// patch releases are additive.
const (
	SupportedAPIMajor = 0
	SupportedAPIMinor = 3
)

// EventType distinguishes subscriber events.
type EventType int

const (
	// EventEngine reports a change of the engine connection status.
	EventEngine EventType = iota
	// EventTelemetry carries a telemetry sample.
	EventTelemetry
	// EventLog carries an engine log entry.
	EventLog
)

// Event is delivered to subscribers. The proto messages are shared between
// subscribers and must not be modified.
type Event struct {
	Type      EventType
	Connected bool                // EventEngine
	Telemetry *enginev1.Telemetry // EventTelemetry
	Log       *enginev1.LogEntry  // EventLog
}

// Options configures a Client. Zero values select defaults.
type Options struct {
	// Target is the engine address: a gRPC target such as
	// unix:///run/motioncore/engine.sock, or a plain host:port.
	Target           string
	RequestTimeout   time.Duration // per-call deadline, default 5s
	MinBackoff       time.Duration // default 250ms
	MaxBackoff       time.Duration // default 5s
	SubscriberBuffer int           // per-subscriber channel size, default 64
	Logger           *slog.Logger
	// DialOptions are appended to the default dial options.
	DialOptions []grpc.DialOption
}

// Client is the engine client. Create it with New and start it with Run.
type Client struct {
	opts Options
	log  *slog.Logger
	conn *grpc.ClientConn

	// Engine and Firmware are the raw generated clients. Prefer Call, which
	// applies the request timeout, the connection gate and error translation.
	Engine   enginev1.EngineServiceClient
	Firmware enginev1.FirmwareServiceClient

	mu           sync.Mutex
	connected    bool                 // handshake done and event stream live
	info         *enginev1.EngineInfo // from the handshake, nil while disconnected
	incompatible error                // last handshake refused for version mismatch
	lastErr      error                // why the last session attempt failed or ended
	latest       *enginev1.Telemetry  // nil while disconnected
	subs         map[*subscriber]struct{}
}

type subscriber struct{ ch chan Event }

// Target converts an address into a gRPC target. Addresses that already have
// a scheme (unix:, dns:, passthrough:, ...) are used unchanged; a plain
// host:port becomes passthrough:///host:port.
func Target(addr string) string {
	if i := strings.Index(addr, ":"); i > 0 {
		scheme := addr[:i]
		switch scheme {
		case "unix", "unix-abstract", "dns", "passthrough", "ipv4", "ipv6":
			return addr
		}
	}
	return "passthrough:///" + addr
}

// New creates a client. It does not connect; call Run.
func New(opts Options) (*Client, error) {
	if opts.Target == "" {
		return nil, errors.New("engine: empty target")
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 5 * time.Second
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 250 * time.Millisecond
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = max(5*time.Second, opts.MinBackoff)
	}
	if opts.SubscriberBuffer < 2 {
		opts.SubscriberBuffer = 64
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	target := Target(opts.Target)
	dialOpts := append([]grpc.DialOption{
		// The engine listens on a local socket or loopback TCP only.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Keep gRPC's own reconnect backoff in line with ours (its default
		// grows to 120s).
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  opts.MinBackoff,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   opts.MaxBackoff,
			},
			MinConnectTimeout: opts.RequestTimeout,
		}),
	}, opts.DialOptions...)
	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	return &Client{
		opts:     opts,
		log:      opts.Logger.With("component", "engine", "target", target),
		conn:     conn,
		Engine:   enginev1.NewEngineServiceClient(conn),
		Firmware: enginev1.NewFirmwareServiceClient(conn),
		subs:     make(map[*subscriber]struct{}),
	}, nil
}

// Close releases the gRPC channel. Run must have returned.
func (c *Client) Close() error { return c.conn.Close() }

// Connected reports whether the handshake succeeded and the event stream is
// live.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Info returns the engine info from the handshake, or nil while disconnected.
func (c *Client) Info() *enginev1.EngineInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return nil
	}
	return c.info
}

// LastError explains why the engine is not connected (version mismatch,
// unreachable, stream ended), or nil while connected / before any attempt.
func (c *Client) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	if c.incompatible != nil {
		return c.incompatible
	}
	return c.lastErr
}

// unavailable returns the error reported to callers while disconnected.
func (c *Client) unavailable() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.incompatible != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, c.incompatible)
	}
	return ErrUnavailable
}

// Run maintains the engine session, reconnecting with exponential backoff,
// until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	delay := c.opts.MinBackoff
	for {
		established, err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.lastErr = err
		c.mu.Unlock()
		if established {
			delay = c.opts.MinBackoff
			c.log.Warn("engine connection lost", "err", err)
		} else if errors.Is(err, errIncompatible) {
			c.log.Error("refusing engine", "err", err, "retry_in", delay)
		} else {
			c.log.Debug("engine unavailable", "err", err, "retry_in", delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, c.opts.MaxBackoff)
	}
}

var errIncompatible = errors.New("incompatible engine api_version")

// CheckAPIVersion verifies that v (semver "MAJOR.MINOR.PATCH") is a version
// this server supports.
func CheckAPIVersion(v string) error {
	want := fmt.Sprintf("%d.x", SupportedAPIMajor)
	if SupportedAPIMajor == 0 {
		want = fmt.Sprintf("0.%d.x", SupportedAPIMinor)
	}
	majorStr, rest, _ := strings.Cut(v, ".")
	minorStr, _, _ := strings.Cut(rest, ".")
	major, err1 := strconv.Atoi(majorStr)
	minor, err2 := strconv.Atoi(minorStr)
	if err1 != nil || err2 != nil || major != SupportedAPIMajor || (major == 0 && minor != SupportedAPIMinor) {
		return fmt.Errorf("%w %q (this server requires %s)", errIncompatible, v, want)
	}
	return nil
}

// session runs one handshake + event stream lifetime. It reports whether the
// stream was established.
func (c *Client) session(ctx context.Context) (bool, error) {
	ictx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	info, err := c.Engine.GetInfo(ictx, &enginev1.GetInfoRequest{})
	cancel()
	if err != nil {
		c.mu.Lock()
		c.incompatible = nil
		c.mu.Unlock()
		return false, fmt.Errorf("GetInfo: %w", err)
	}
	if err := CheckAPIVersion(info.GetApiVersion()); err != nil {
		c.mu.Lock()
		c.incompatible = err
		c.mu.Unlock()
		return false, err
	}

	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	stream, err := c.Engine.StreamEvents(sctx, &enginev1.StreamEventsRequest{})
	if err != nil {
		return false, fmt.Errorf("StreamEvents: %w", err)
	}
	// The first event is a telemetry snapshot; don't wait forever for it.
	firstTimer := time.AfterFunc(c.opts.RequestTimeout, scancel)
	first, err := stream.Recv()
	if !firstTimer.Stop() {
		return false, errors.New("StreamEvents: no initial event")
	}
	if err != nil {
		return false, fmt.Errorf("StreamEvents: %w", err)
	}

	c.mu.Lock()
	c.connected, c.info, c.incompatible = true, info, nil
	c.broadcast(Event{Type: EventEngine, Connected: true})
	c.handle(first)
	c.mu.Unlock()
	c.log.Info("engine connected", "api_version", info.GetApiVersion(), "engine_version", info.GetEngineVersion())

	for {
		ev, err := stream.Recv()
		if err != nil {
			c.teardown()
			return true, err
		}
		c.mu.Lock()
		c.handle(ev)
		c.mu.Unlock()
	}
}

// handle caches and broadcasts one stream event. c.mu must be held.
func (c *Client) handle(ev *enginev1.Event) {
	switch e := ev.GetEvent().(type) {
	case *enginev1.Event_Telemetry:
		c.latest = e.Telemetry
		c.broadcast(Event{Type: EventTelemetry, Telemetry: e.Telemetry})
	case *enginev1.Event_Log:
		c.broadcast(Event{Type: EventLog, Log: e.Log})
	}
}

func (c *Client) teardown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	wasConnected := c.connected
	c.connected, c.info, c.latest = false, nil, nil
	if wasConnected {
		c.broadcast(Event{Type: EventEngine, Connected: false})
	}
}

// Subscribe registers a subscriber. The channel first receives the current
// engine connection status and, if available, the latest telemetry, followed
// by live events. If the subscriber falls behind (buffer full) its channel is
// closed. Call cancel to unsubscribe.
func (c *Client) Subscribe() (events <-chan Event, cancel func()) {
	s := &subscriber{ch: make(chan Event, c.opts.SubscriberBuffer)}
	c.mu.Lock()
	s.ch <- Event{Type: EventEngine, Connected: c.connected}
	if c.connected && c.latest != nil {
		s.ch <- Event{Type: EventTelemetry, Telemetry: c.latest}
	}
	c.subs[s] = struct{}{}
	c.mu.Unlock()
	return s.ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, ok := c.subs[s]; ok {
			delete(c.subs, s)
			close(s.ch)
		}
	}
}

// broadcast delivers ev to all subscribers without blocking. c.mu must be held.
func (c *Client) broadcast(ev Event) {
	for s := range c.subs {
		select {
		case s.ch <- ev:
		default:
			delete(c.subs, s)
			close(s.ch)
			c.log.Warn("dropping slow subscriber")
		}
	}
}
