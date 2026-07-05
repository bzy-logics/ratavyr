// Command ratavyr is the MotionCore management server: it exposes
// the engine's gRPC control API as REST/WebSocket (docs/rest-api.md) and
// optionally serves the web UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bzyfuzy/ratavyr/internal/api"
	"github.com/bzyfuzy/ratavyr/internal/config"
	"github.com/bzyfuzy/ratavyr/internal/engine"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load(os.Getenv)
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng, err := engine.New(engine.Options{Target: cfg.EngineAddr, Logger: log})
	if err != nil {
		return err
	}
	defer eng.Close()
	engDone := make(chan struct{})
	go func() { eng.Run(ctx); close(engDone) }()

	srv := &http.Server{
		Addr: cfg.APIAddr,
		Handler: api.New(api.Options{
			Engine:         eng,
			Log:            log,
			Token:          cfg.APIToken,
			WebDir:         cfg.WebDir,
			OriginPatterns: cfg.AllowedOrigins,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "engine", cfg.EngineAddr, "auth", cfg.APIToken != "", "version", api.ServerVersion)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		stop()
		<-engDone
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-engDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}
