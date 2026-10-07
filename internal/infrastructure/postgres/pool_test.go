package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplyPoolConfig(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PG_HEALTH_CHECK_PERIOD", "10s")
	applyPoolConfig(cfg, Options{MaxConns: 8, MinConns: 50, StatementTimeout: 5 * time.Second})
	if cfg.MaxConns != 8 || cfg.MinConns != 8 {
		t.Errorf("conns = %d/%d, want 8/8 (min clamped to max)", cfg.MinConns, cfg.MaxConns)
	}
	if cfg.HealthCheckPeriod != 10*time.Second {
		t.Errorf("health check = %v", cfg.HealthCheckPeriod)
	}
	if cfg.MaxConnIdleTime != defaultMaxConnIdleTime || cfg.MaxConnLifetime != defaultMaxConnLifetime {
		t.Errorf("idle/lifetime = %v/%v", cfg.MaxConnIdleTime, cfg.MaxConnLifetime)
	}
	rp := cfg.ConnConfig.RuntimeParams
	if rp["statement_timeout"] != "5000" || rp["idle_in_transaction_session_timeout"] != "30000" {
		t.Errorf("runtime params = %v", rp)
	}
}
