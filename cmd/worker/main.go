// Command worker runs background jobs (thumbnails, fan-out, email,
// scheduled publishing) without serving the API, so heavy jobs cannot take
// the API down. Run the API with WORKER=false next to it.
package main

import (
	"context"
	"log/slog"
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
)

func main() {
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
	cfg.Worker = true
	cfg.MigrateOnStart = false // the API (or cmd/migrate) owns migrations

	shutdownTelemetry, err := telemetry.Setup(ctx)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(context.Background())
	logger := telemetry.NewLogger()
	defer logger.Sync()
	zap.ReplaceGlobals(logger)
	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core())))

	a, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Start(); err != nil {
		return err
	}

	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", a.Metrics)
		srv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go srv.ListenAndServe()
		defer srv.Close()
	}
	zap.L().Info("worker running")
	<-ctx.Done()
	zap.L().Info("worker stopping")
	return nil
}
