// Command worker runs background jobs (thumbnails, fan-out, email,
// scheduled publishing) without serving the API, so heavy jobs cannot take
// the API down. Run the API with WORKER=false next to it.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/iBlog/iblog-monolith-go/app/core"
	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/queue"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/telemetry"
)

func main() {
	// -healthcheck probes the running worker (distroless images have no curl).
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
	cfg.MigrateOnStart = false // cmd/migrate (or the API) own app + Guard migrations and the seed

	shutdownTelemetry, err := telemetry.Setup(ctx)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(context.Background())
	logger := telemetry.NewLogger()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)
	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core())))

	s, err := core.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	w := queue.NewWorker(s.RDB, s.Handlers())
	if err := w.Start(); err != nil {
		return err
	}
	defer w.Stop()

	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", promhttp.Handler())
		// Liveness for -healthcheck: the worker has no public listener.
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		srv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go srv.ListenAndServe()
		defer srv.Close()
	}
	zap.L().Info("worker running")
	<-ctx.Done()
	zap.L().Info("worker stopping")
	return nil
}

// healthcheck probes /healthz on the private METRICS_ADDR listener; with
// METRICS_ADDR empty the worker has no listener and reports unhealthy.
func healthcheck() int {
	addr, ok := os.LookupEnv("METRICS_ADDR")
	if !ok {
		addr = ":9090"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return 1
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
