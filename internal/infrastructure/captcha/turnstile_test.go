package captcha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("secret") != "s" {
			t.Errorf("secret = %q", r.Form.Get("secret"))
		}
		if r.Form.Get("response") == "good" {
			w.Write([]byte(`{"success":true}`))
			return
		}
		w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer srv.Close()
	ts := Turnstile{Secret: "s", URL: srv.URL}
	ctx := context.Background()

	if err := ts.Verify(ctx, "good", "1.2.3.4"); err != nil {
		t.Fatalf("good token: %v", err)
	}
	if err := ts.Verify(ctx, "bad", ""); !errors.Is(err, ErrFailed) {
		t.Fatalf("bad token: %v", err)
	}
	if err := ts.Verify(ctx, "", ""); !errors.Is(err, ErrFailed) {
		t.Fatalf("empty token: %v", err)
	}

	down := Turnstile{Secret: "s", URL: "http://127.0.0.1:1"}
	if err := down.Verify(ctx, "good", ""); err == nil || errors.Is(err, ErrFailed) {
		t.Fatalf("unreachable: %v", err)
	}
}
