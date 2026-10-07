// Command migrate applies database migrations and exits. Run it as a
// release job and set MIGRATE_ON_START=false on the API replicas.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/config"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/postgres"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/telemetry"
)

func main() {
	zap.ReplaceGlobals(telemetry.NewLogger())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err == nil {
		err = postgres.Migrate(ctx, cfg.DatabaseURL)
	}
	if err != nil {
		zap.L().Error("migrate", zap.Error(err))
		os.Exit(1)
	}
	zap.L().Info("migrations applied")
}
