package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/bakhod1r/guard/ratelimit"
)

func TestLimitWithFailsClosedOnlyForSensitiveKeys(t *testing.T) {
	var gotKey string
	var gotLimit int
	up := limitWith(1, func(_ context.Context, key string, rule ratelimit.Rule) (bool, time.Duration, error) {
		gotKey, gotLimit = key, rule.Limit
		return false, 7 * time.Second, nil
	})
	if ok, retry := up(context.Background(), "like:1.2.3.4", 60, time.Minute); ok || retry != 7*time.Second {
		t.Errorf("backend verdict changed: %v %v", ok, retry)
	}
	if gotKey != "blog:like:1.2.3.4" || gotLimit != 60 {
		t.Errorf("key %q limit %d", gotKey, gotLimit)
	}

	scaled := limitWith(20, func(_ context.Context, _ string, rule ratelimit.Rule) (bool, time.Duration, error) {
		gotLimit = rule.Limit
		return true, 0, nil
	})
	if scaled(context.Background(), "login:ip", 10, time.Minute); gotLimit != 200 {
		t.Errorf("multiplier 20: limit %d, want 200", gotLimit)
	}

	down := limitWith(1, func(context.Context, string, ratelimit.Rule) (bool, time.Duration, error) {
		return true, 0, errors.New("redis down")
	})
	for _, k := range sensitiveLimits {
		if ok, retry := down(context.Background(), k+":ip", 5, time.Minute); ok || retry != 30*time.Second {
			t.Errorf("%s with Redis down: allowed=%v retry=%v (must fail closed)", k, ok, retry)
		}
	}
	for _, k := range []string{"like:ip", "view:ip", "upload:ip"} {
		if ok, _ := down(context.Background(), k, 5, time.Minute); !ok {
			t.Errorf("%s with Redis down: blocked (should fail open)", k)
		}
	}
}

func TestDocsDisabledInProductionWithoutKey(t *testing.T) {
	mux := http.NewServeMux()
	mountDocs(mux, config.Config{Env: "production"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/docs/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("docs served without DOCS_KEY: %d", rec.Code)
	}
}
