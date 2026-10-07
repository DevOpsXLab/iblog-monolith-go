package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireBearer(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireBearer("0123456789abcdef0123456789abcdef", ok)
	for _, tc := range []struct {
		auth string
		want int
	}{
		{"", http.StatusNotFound},
		{"Bearer wrong", http.StatusNotFound},
		{"0123456789abcdef0123456789abcdef", http.StatusNotFound},
		{"Bearer 0123456789abcdef0123456789abcdef", http.StatusOK},
	} {
		r := httptest.NewRequest("GET", "/metrics", nil)
		if tc.auth != "" {
			r.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("auth %q: status %d, want %d", tc.auth, w.Code, tc.want)
		}
	}
}

func TestWithoutGuardLoginHidesPasswordChange(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := withoutGuardLogin(ok)
	for path, want := range map[string]int{
		"/api/guard/auth/login":    http.StatusNotFound,
		"/api/guard/auth/register": http.StatusNotFound,
		"/api/guard/auth/password": http.StatusNotFound,
		"/api/guard/auth/logout":   http.StatusOK,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PUT", path, nil))
		if w.Code != want {
			t.Errorf("%s: status %d, want %d", path, w.Code, want)
		}
	}
}
