package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadFromEnv(t *testing.T) {
	t.Chdir(t.TempDir()) // no stray .env
	t.Setenv("APP_ENV", "dev")
	t.Setenv("PORT", "9999")
	t.Setenv("DB_STATEMENT_TIMEOUT", "2s")
	t.Setenv("WORKER", "false")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "9999" || c.DBStatementTimeout != 2*time.Second || c.Worker || !c.Dev() {
		t.Errorf("cfg = %+v", c)
	}
	if c.MFAKey != DevMFAKey || c.UnsubscribeKey != DevUnsubscribeKey {
		t.Error("dev keys not filled")
	}
	if c.S3Bucket != "uploads" {
		t.Errorf("default S3_BUCKET = %q", c.S3Bucket)
	}
}

func TestLoadProductionRefusesDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "production")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MFA_KEY") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadBadValue(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DB_MAX_CONNS", "lots")
	if _, err := Load(); err == nil {
		t.Fatal("bad int accepted")
	}
}

func TestValidateEnvName(t *testing.T) {
	c := Config{Env: "staging"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateProductionWeakSecrets(t *testing.T) {
	c := prodConfig()
	c.AdminPassword = "short"
	c.S3SecretKey = "minio12345"
	c.DatabaseURL = "postgres://blog:blog@db/blog"
	err := c.Validate()
	if err == nil {
		t.Fatal("weak secrets accepted")
	}
	for _, want := range []string{"ADMIN_PASSWORD", "S3_SECRET_KEY", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %s: %v", want, err)
		}
	}
	ok := prodConfig()
	if err := ok.Validate(); err != nil {
		t.Fatalf("strong config refused: %v", err)
	}
}

func TestTrustedProxyPrefixes(t *testing.T) {
	c := Config{TrustedProxies: " 10.0.0.0/8 , ,::1/128"}
	p, err := c.TrustedProxyPrefixes()
	if err != nil || len(p) != 2 || p[0].String() != "10.0.0.0/8" || p[1].String() != "::1/128" {
		t.Fatalf("prefixes = %v, %v", p, err)
	}
	c.TrustedProxies = "10.0.0.1"
	if _, err := c.TrustedProxyPrefixes(); err == nil {
		t.Fatal("address without prefix length accepted")
	}
	bad := Config{Env: "dev", TrustedProxies: "nope"}
	if err := bad.Validate(); err == nil {
		t.Fatal("Validate ignores bad TRUSTED_PROXIES")
	}
}

func TestAllowedOrigins(t *testing.T) {
	c := Config{SiteURL: "https://blog.example/"}
	if got := c.AllowedOrigins(); !slices.Equal(got, []string{"https://blog.example", "http://localhost:8082"}) {
		t.Errorf("default origins = %v", got)
	}
	c.CORSOrigins = "https://a.example, https://b.example"
	if got := c.AllowedOrigins(); !slices.Equal(got, []string{"https://a.example", "https://b.example"}) {
		t.Errorf("CORS_ORIGINS = %v", got)
	}
}
