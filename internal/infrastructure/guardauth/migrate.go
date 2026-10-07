package guardauth

import (
	"context"
	"fmt"

	"github.com/bakhod1r/guard"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// setupLockID is the Postgres advisory lock key held while setup runs.
// Guard's migrations take their own lock (migrations.LockID), but the seed
// is a read-then-insert on unique policy names, so replicas starting
// together must not run it at once.
const setupLockID int64 = 0x69626c6f675f7367 // "iblog_sg"

// Migrate applies Guard's migrations and seeds the blog's access rules, then
// closes Guard. Run it after postgres.Migrate: Guard references the users
// table. Every step is idempotent, so a second run is a no-op.
func Migrate(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, o Options) error {
	o.Migrate = true
	g, err := Open(ctx, db, rdb, o)
	if err != nil {
		return err
	}
	return g.Close(ctx)
}

// setup runs Guard's schema migrations (DDL) and then the access seed (DML)
// under one session advisory lock on a dedicated connection, so concurrent
// cmd/migrate runs and replicas with MIGRATE_ON_START=true run it one at a
// time. cmd/migrate (via Migrate) and app start (via Open) share this path.
func setup(ctx context.Context, db *pgxpool.Pool, g *guard.Guard, rules AccessRules) (err error) {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Waiting for another replica may outlast the pool's statement_timeout.
	if _, err := conn.Exec(ctx, "SET statement_timeout = 0"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", setupLockID); err != nil {
		conn.Conn().Close(context.Background()) // drop the session and its settings
		return fmt.Errorf("guard setup lock: %w", err)
	}
	defer func() {
		if _, uerr := conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1); RESET statement_timeout"); uerr != nil {
			// Closing the session releases the lock; the pool discards it.
			conn.Conn().Close(context.Background())
		}
	}()

	if err := migrateSchema(ctx, g); err != nil {
		return err
	}
	return seedAccess(ctx, g, rules)
}

// migrateSchema applies Guard's own migrations (guard_schema_version). They
// reference the users table, so postgres.Migrate must have run first.
func migrateSchema(ctx context.Context, g *guard.Guard) error {
	if err := g.Migrate(ctx, ""); err != nil {
		return fmt.Errorf("guard migrate: %w", err)
	}
	return nil
}

// seedAccess writes the blog's permissions, roles and policies (Seed, which
// also runs MigrateRoleConditions) and the config-driven access rules. Each
// step is an upsert or an existence check, so a re-run changes nothing.
func seedAccess(ctx context.Context, g *guard.Guard, rules AccessRules) error {
	if err := Seed(ctx, g); err != nil {
		return fmt.Errorf("seed access: %w", err)
	}
	if err := SeedAccessRules(ctx, g, rules); err != nil {
		return fmt.Errorf("access rules: %w", err)
	}
	return nil
}
