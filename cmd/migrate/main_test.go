package main

// Integration test: cmd/migrate against a fresh Postgres (testcontainers).
// Skipped with -short or without Docker.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/iBlog/iblog-monolith-go/app"
	"github.com/iBlog/iblog-monolith-go/config"
)

// counts are the rows migrate writes; a re-run must not change them.
const counts = `SELECT
	(SELECT count(*) FROM goose_db_version),
	(SELECT count(*) FROM guard_schema_version),
	(SELECT count(*) FROM guard_permission),
	(SELECT count(*) FROM guard_role),
	(SELECT count(*) FROM guard_role_permission),
	(SELECT count(*) FROM guard_policy),
	(SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'guard\_%')`

func snapshot(t *testing.T, db *pgxpool.Pool) [7]int {
	t.Helper()
	var c [7]int
	if err := db.QueryRow(context.Background(), counts).Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[6]); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMigrate(t *testing.T) {
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
	must(t, err)
	rd, err := tcredis.Run(ctx, "redis:8-alpine")
	must(t, err)
	t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
	redisURL, err := rd.ConnectionString(ctx)
	must(t, err)

	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("REDIS_URL", redisURL)
	t.Setenv("PASSWORD_HASH_MEMORY_KIB", "19456")
	t.Setenv("PASSWORD_HASH_TIME", "2")
	t.Setenv("PASSWORD_HASH_THREADS", "1")

	// Three concurrent runs on a fresh database serialise on the locks.
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Go(func() { errs[i] = run(ctx, "all") })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	db, err := pgxpool.New(ctx, dsn)
	must(t, err)
	t.Cleanup(db.Close)
	concurrent := snapshot(t, db)
	if concurrent[0] == 0 || concurrent[1] == 0 || concurrent[6] == 0 {
		t.Fatalf("schema missing: %v", concurrent)
	}

	// A single run on another fresh database gives the same counts.
	_, err = db.Exec(ctx, "CREATE DATABASE single")
	must(t, err)
	singleDSN := strings.Replace(dsn, "/blog?", "/single?", 1)
	t.Setenv("DATABASE_URL", singleDSN)
	must(t, run(ctx, "all"))
	single, err := pgxpool.New(ctx, singleDSN)
	must(t, err)
	t.Cleanup(single.Close)
	if got := snapshot(t, single); got != concurrent {
		t.Fatalf("concurrent %v != single %v", concurrent, got)
	}

	// A second run is a no-op.
	t.Setenv("DATABASE_URL", dsn)
	must(t, run(ctx, "all"))
	if got := snapshot(t, db); got != concurrent {
		t.Fatalf("re-run changed counts: %v -> %v", concurrent, got)
	}

	// The app boots with MIGRATE_ON_START=false as a DML-only user: any DDL
	// at start would fail with "permission denied".
	for _, q := range []string{
		"CREATE ROLE app LOGIN PASSWORD 'app'",
		"GRANT USAGE ON SCHEMA public TO app",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app",
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO app",
	} {
		_, err := db.Exec(ctx, q)
		must(t, err)
	}
	mn, err := tcminio.Run(ctx, "cgr.dev/chainguard/minio:latest", tcminio.WithUsername("minio"), tcminio.WithPassword("minio12345"))
	must(t, err)
	t.Cleanup(func() { testcontainers.TerminateContainer(mn) })
	s3, err := mn.ConnectionString(ctx)
	must(t, err)
	a, err := app.New(ctx, config.Config{
		Env: "dev", MFAKey: config.DevMFAKey, UnsubscribeKey: config.DevUnsubscribeKey,
		MigrateOnStart: false, DBMaxConns: 5,
		DatabaseURL: strings.Replace(dsn, "blog:blog@", "app:app@", 1), RedisURL: redisURL,
		AdminUsername: "admin", AdminEmail: "admin@iblog.dev", AdminPassword: "admin-pass-123",
		S3Endpoint: s3, S3AccessKey: "minio", S3SecretKey: "minio12345", S3Bucket: "uploads",
		SiteURL: "http://site.test", PublicURL: "http://localhost", DocsDir: "../..",
		PasswordHashMemoryKiB: 19456, PasswordHashTime: 2, PasswordHashThreads: 1,
	})
	must(t, err)
	t.Cleanup(a.Close)
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	body, _ := json.Marshal(map[string]string{"login": "admin", "password": "admin-pass-123"})
	res, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	must(t, err)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", res.StatusCode)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
