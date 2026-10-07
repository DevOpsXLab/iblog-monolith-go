package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedBypassesCache(t *testing.T) {
	cases := map[string]func(*http.Request){
		"bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") },
		"api key": func(r *http.Request) { r.Header.Set("X-API-Key", "k") },
		"session": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "guard_session", Value: "s"}) },
	}
	for name, set := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
		set(r)
		if !authenticated(r) {
			t.Errorf("%s: not treated as authenticated", name)
		}
	}
	if authenticated(httptest.NewRequest(http.MethodGet, "/api/posts", nil)) {
		t.Error("anonymous request treated as authenticated")
	}
}

func TestSkipInvalidate(t *testing.T) {
	SkipInvalidate(context.Background()) // outside Cache: no-op, no panic
	flag := new(bool)
	SkipInvalidate(context.WithValue(context.Background(), skipInvalidateKey{}, flag))
	if !*flag {
		t.Fatal("flag not set")
	}
}
