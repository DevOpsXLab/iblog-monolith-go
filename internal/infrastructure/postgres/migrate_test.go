package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/postgres"
)

func TestLatestMigration(t *testing.T) {
	v, err := postgres.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if v < 33 {
		t.Fatalf("latest = %d, want >= 33 (00033_analytics.sql)", v)
	}
}

// Every Down section must work: up, down to an older version, up again.
func TestMigrateDownToRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	version := func() int64 {
		t.Helper()
		db, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var v int64
		if err := db.QueryRow(ctx, `SELECT max(version_id) FROM migration_version WHERE is_applied`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	latest, err := postgres.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if v := version(); v != latest {
		t.Fatalf("after up: version %d, want %d", v, latest)
	}
	if err := postgres.MigrateDownTo(ctx, dsn, 1); err != nil {
		t.Fatalf("down to 1: %v", err)
	}
	if v := version(); v != 1 {
		t.Fatalf("after down: version %d, want 1", v)
	}
	if err := postgres.Migrate(ctx, dsn); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v := version(); v != latest {
		t.Fatalf("after re-up: version %d, want %d", v, latest)
	}
}
