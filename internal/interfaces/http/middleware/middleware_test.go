package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestCORS(t *testing.T) {
	h := CORS([]string{"http://site.test/"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cases := []struct {
		origin, allow string
	}{
		{"http://site.test", "http://site.test"},
		{"http://evil.test", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodOptions, "/api/me", nil)
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Access-Control-Request-Method", "PATCH")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != c.allow {
			t.Errorf("origin %s: allow-origin %q, want %q", c.origin, got, c.allow)
		}
		if c.allow != "" && w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Errorf("origin %s: no allow-methods", c.origin)
		}
		if w.Code != http.StatusNoContent {
			t.Errorf("preflight status %d", w.Code)
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		name   string
		trust  bool
		remote string
		header string
		want   string
	}{
		{"no trust", false, "10.1.1.1:5000", "1.2.3.4", "10.1.1.1"},
		{"trusted proxy", true, "10.1.1.1:5000", "1.2.3.4", "1.2.3.4"},
		{"untrusted peer spoofs", true, "8.8.8.8:5000", "1.2.3.4", "8.8.8.8"},
		{"garbage header", true, "10.1.1.1:5000", "nope", "10.1.1.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		r.Header.Set("X-Real-IP", c.header)
		if got := ClientIP(c.trust, trusted)(r); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCacheKeyURI(t *testing.T) {
	a := httptest.NewRequest(http.MethodGet, "/api/posts?tag=go&page=2", nil)
	b := httptest.NewRequest(http.MethodGet, "/api/posts?page=2&tag=go", nil)
	if cacheKeyURI(a) != cacheKeyURI(b) {
		t.Fatalf("%s != %s", cacheKeyURI(a), cacheKeyURI(b))
	}
	if !sessionOnly("/api/auth/login") || sessionOnly("/api/posts") {
		t.Fatal("sessionOnly")
	}
}

func TestKnownQuery(t *testing.T) {
	for url, want := range map[string]bool{
		"/api/posts":                 true,
		"/api/posts?tag=go&page=2":   true,
		"/api/posts?tag=go&junk=123": false,
		"/api/posts?_=1700000000":    false,
	} {
		if got := knownQuery(httptest.NewRequest(http.MethodGet, url, nil)); got != want {
			t.Errorf("%s: %v, want %v", url, got, want)
		}
	}
}
