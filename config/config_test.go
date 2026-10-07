package config

import (
	"strings"
	"testing"
)

func prodConfig() Config {
	return Config{
		Env:         "production",
		DatabaseURL: "postgres://u:s3cret-long-password@db/blog",
		S3SecretKey: "a-real-s3-secret-value",
		MFAKey:      strings.Repeat("m", 32),
	}
}

func TestValidateProductionRequiresMFAKey(t *testing.T) {
	c := prodConfig()
	c.MFAKey = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MFA_KEY") {
		t.Fatalf("missing MFA_KEY: err = %v", err)
	}
	c.MFAKey = DevMFAKey
	if err := c.Validate(); err == nil {
		t.Fatal("dev MFA_KEY accepted in production")
	}
}

func TestValidateUnsubscribeKeyOptionalButStrong(t *testing.T) {
	c := prodConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("empty UNSUBSCRIBE_KEY (legacy fallback): %v", err)
	}
	c.UnsubscribeKey = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("weak UNSUBSCRIBE_KEY accepted")
	}
}

func TestValidateMetricsToken(t *testing.T) {
	c := prodConfig()
	c.MetricsToken = "short"
	if err := c.Validate(); err == nil {
		t.Fatal("weak METRICS_TOKEN accepted")
	}
}

func TestValidateDevFillsKeys(t *testing.T) {
	c := Config{Env: "dev"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.MFAKey != DevMFAKey {
		t.Fatalf("MFAKey = %q", c.MFAKey)
	}
}
