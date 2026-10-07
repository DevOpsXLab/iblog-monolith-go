// Package postgres implements domain repositories with PostgreSQL (pgx).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/migrations"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Options tune the pool. Zero values fall back to env (PG_*) and then to
// the defaults below; StatementTimeout is applied as given (0 disables),
// matching config.DBStatementTimeout.
type Options struct {
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	StatementTimeout  time.Duration
	// IdleInTxTimeout ends sessions idle inside a transaction (lock holders).
	IdleInTxTimeout time.Duration
	// Migrate applies goose migrations after connecting.
	Migrate bool
}

// Connect opens a pool and, with opts.Migrate, applies goose migrations.
func Connect(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	applyPoolConfig(cfg, opts)
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := otelpgx.RecordStats(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres stats: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if !opts.Migrate {
		return db, nil
	}
	if err := Migrate(ctx, url); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// Pool defaults for zero Options fields; env overrides: PG_MAX_CONNS,
// PG_MIN_CONNS, PG_MAX_CONN_LIFETIME, PG_MAX_CONN_IDLE_TIME,
// PG_HEALTH_CHECK_PERIOD, PG_IDLE_IN_TX_TIMEOUT.
const (
	defaultMaxConns          = 20
	defaultMinConns          = 2
	defaultMaxConnLifetime   = 30 * time.Minute
	defaultMaxConnIdleTime   = 5 * time.Minute
	defaultHealthCheckPeriod = 30 * time.Second
	defaultIdleInTxTimeout   = 30 * time.Second
)

func applyPoolConfig(cfg *pgxpool.Config, o Options) {
	cfg.MaxConns = or(o.MaxConns, int32(envInt("PG_MAX_CONNS", defaultMaxConns)))
	cfg.MinConns = min(or(o.MinConns, int32(envInt("PG_MIN_CONNS", defaultMinConns))), cfg.MaxConns)
	cfg.MaxConnLifetime = or(o.MaxConnLifetime, envDuration("PG_MAX_CONN_LIFETIME", defaultMaxConnLifetime))
	cfg.MaxConnIdleTime = or(o.MaxConnIdleTime, envDuration("PG_MAX_CONN_IDLE_TIME", defaultMaxConnIdleTime))
	cfg.HealthCheckPeriod = or(o.HealthCheckPeriod, envDuration("PG_HEALTH_CHECK_PERIOD", defaultHealthCheckPeriod))
	rp := cfg.ConnConfig.RuntimeParams
	rp["statement_timeout"] = ms(max(o.StatementTimeout, 0))
	rp["idle_in_transaction_session_timeout"] = ms(or(o.IdleInTxTimeout, envDuration("PG_IDLE_IN_TX_TIMEOUT", defaultIdleInTxTimeout)))
}

func or[T int32 | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return def
}

// Migrate applies pending migrations on its own small pool (no
// statement_timeout, so long index builds finish). A Postgres advisory lock
// serialises replicas that start at the same time.
func Migrate(ctx context.Context, url string) error {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(db), migrations.FS,
		goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	defer provider.Close()
	_, err = provider.Up(ctx)
	return err
}

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func exists(ctx context.Context, db *pgxpool.Pool, sql string, id int) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, sql, id).Scan(&ok)
	return ok, err
}

func deleteByID(ctx context.Context, db *pgxpool.Pool, sql string, id int) error {
	tag, err := db.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func constraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func ignoreNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// execOne runs sql and returns domain.ErrNotFound when no row changed.
func execOne(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) error {
	tag, err := db.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
