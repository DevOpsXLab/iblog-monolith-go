package guardauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEnvironment(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.Header.Set("Origin", "http://localhost:8082")
	now := time.Date(2026, 10, 6, 13, 30, 0, 0, time.UTC) // 18:30 in Tashkent, a Tuesday
	env := Environment(r, func(*http.Request) string { return "203.0.113.7" }, loc, now)
	want := map[string]any{
		"ip": "203.0.113.7", "time": "18:30", "weekday": "tue", "date": "2026-10-06",
		"now": "2026-10-06T13:30:00Z", "method": "GET", "origin": "http://localhost:8082",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env.%s = %v, want %v", k, env[k], v)
		}
	}
}

func TestIsSignOut(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/auth/logout": true, "/api/guard/auth/logout-all": true, "/api/me": false,
	} {
		if got := isSignOut(httptest.NewRequest(http.MethodPost, path, nil)); got != want {
			t.Errorf("%s: %v", path, got)
		}
	}
}
