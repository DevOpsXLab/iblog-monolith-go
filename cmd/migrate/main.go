// Command migrate is the single owner of schema migrations and the access
// seed. Run it as a release job before the API/worker rollout and set
// MIGRATE_ON_START=false on the replicas, so they can use a DML-only DB user.
//
//	go run ./cmd/migrate                # app schema, then Guard schema + access seed
//	go run ./cmd/migrate -target app    # only the app schema (goose, goose_db_version)
//	go run ./cmd/migrate -target guard  # only Guard schema (guard_schema_version) + seed
//
// Order is fixed: Guard references the users table, so the app schema runs
// first. Both steps hold Postgres advisory locks and are idempotent, so
// concurrent runs serialise and a second run is a no-op.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/guardauth"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/redis"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/telemetry"
)

func main() {
	target := flag.String("target", "all", "what to migrate: all, app or guard")
	flag.Parse()
	zap.ReplaceGlobals(telemetry.NewLogger())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *target); err != nil {
		zap.L().Error("migrate", zap.Error(err))
		os.Exit(1)
	}
	zap.L().Info("migrations applied", zap.String("target", *target))
}

func run(ctx context.Context, target string) error {
	if target != "all" && target != "app" && target != "guard" {
		return fmt.Errorf("unknown -target %q (all, app, guard)", target)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if target != "guard" {
		if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("app schema: %w", err)
		}
	}
	if target == "app" {
		return nil
	}
	return migrateGuard(ctx, cfg)
}

// migrateGuard applies Guard's migrations and the access seed under the setup lock.
func migrateGuard(ctx context.Context, cfg config.Config) error {
	db, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.Options{MaxConns: 4})
	if err != nil {
		return err
	}
	defer db.Close()
	rdb, err := redis.Connect(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()
	if err := guardauth.Migrate(ctx, db, rdb, guardauth.Options{
		HashMemoryKiB: cfg.PasswordHashMemoryKiB,
		HashTime:      cfg.PasswordHashTime,
		HashThreads:   cfg.PasswordHashThreads,
		AuditEmailKey: cfg.AuditEmailKey,
		AccessRules:   guardauth.NewAccessRules(cfg.ModeratorIPs, cfg.ModeratorHours),
	}); err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	return nil
}
