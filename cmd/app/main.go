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

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/DevOpsXLab/iblog-monolith-go/app"
	"github.com/DevOpsXLab/iblog-monolith-go/config"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/telemetry"
	httpapi "github.com/DevOpsXLab/iblog-monolith-go/internal/interfaces/http"
)

func main() {
	// -healthcheck probes the running API (distroless images have no curl).
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}
	zap.ReplaceGlobals(telemetry.NewLogger())
	if err := run(); err != nil {
		zap.L().Error("fatal", zap.Error(err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	shutdownTelemetry, err := telemetry.Setup(ctx)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(context.Background())
	// Rebuild now that OTel is up: adds the OTLP log pipeline when enabled.
	logger := telemetry.NewLogger()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)
	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core()))) // libraries logging via slog

	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Start(); err != nil {
		return err
	}

	// Cancelled when Shutdown starts, so SSE streams return instead of
	// holding Shutdown until its deadline.
	streams, stopStreams := context.WithCancel(context.Background())
	defer stopStreams()
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext: func(net.Listener) context.Context {
			return httpapi.WithShutdown(context.Background(), streams)
		},
	}
	srv.RegisterOnShutdown(stopStreams)
	errc := make(chan error, 2)
	go func() {
		zap.L().Info("listening", zap.String("addr", srv.Addr))
		errc <- srv.ListenAndServe()
	}()

	// /metrics on a private listener: never exposed with the public API.
	var metrics *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", a.Metrics)
		metrics = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			zap.L().Info("metrics listening", zap.String("addr", metrics.Addr))
			if err := metrics.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown: stop accepting, let in-flight requests finish.
	zap.L().Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if metrics != nil {
		defer metrics.Shutdown(shutdownCtx)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// Stragglers past the grace period: cut them and exit cleanly.
		zap.L().Warn("shutdown deadline exceeded, closing connections", zap.Error(err))
		srv.Close()
	}
	zap.L().Info("stopped")
	return nil
}

func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/api/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
