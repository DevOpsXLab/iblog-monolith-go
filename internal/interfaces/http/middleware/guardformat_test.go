package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakhod1r/errorx"
)

func guardReply(status int, ctype, body string) http.Handler {
	return GuardFormat(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func serve(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/guard/x", nil))
	return rec
}

func TestGuardFormatSuccessEnvelope(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"list unwrapped", `{"sessions":[{"id":"a"}]}`, `{"data":[{"id":"a"}]}`},
		{"object kept", `{"id":1,"email":"a@b"}`, `{"data":{"id":1,"email":"a@b"}}`},
		{"single non-list key kept", `{"user":{"id":1}}`, `{"data":{"user":{"id":1}}}`},
		{"empty body", ``, `{"data":null}`},
		{"null body", `null`, `{"data":null}`},
	}
	for _, c := range cases {
		rec := serve(guardReply(201, guardContentType, c.body))
		if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %q", c.name, rec.Code, rec.Header().Get("Content-Type"))
		}
		var got, want any
		json.Unmarshal(rec.Body.Bytes(), &got)
		json.Unmarshal([]byte(c.want), &want)
		if b1, _ := json.Marshal(got); string(b1) != mustJSON(want) {
			t.Errorf("%s: body = %s, want %s", c.name, rec.Body, c.want)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestGuardFormatErrorBecomesProblem(t *testing.T) {
	rec := serve(guardReply(404, guardContentType, `{"error":{"code":"session_not_found","message":"no such session"}}`))
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	if p["code"] != errorx.ErrNotFound || p["detail"] != "no such session" || p["reason"] != "session_not_found" {
		t.Errorf("problem = %v", p)
	}
}

func TestGuardFormatPassesOtherResponses(t *testing.T) {
	rec := serve(guardReply(418, "application/json", `{"mine":true}`))
	if rec.Code != 418 || rec.Body.String() != `{"mine":true}` {
		t.Errorf("blog response altered: %d %s", rec.Code, rec.Body)
	}
	// Implicit 200 on first Write, and Flush reaches the real writer.
	h := GuardFormat(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain"))
		w.WriteHeader(500) // ignored: already decided
		http.NewResponseController(w).Flush()
	}))
	rec = serve(h)
	if rec.Code != 200 || rec.Body.String() != "plain" || !rec.Flushed {
		t.Errorf("passthrough = %d %q flushed=%v", rec.Code, rec.Body, rec.Flushed)
	}
}

func TestGuardProblemCodes(t *testing.T) {
	cases := []struct {
		status   int
		code     string
		wantType string
	}{
		{401, "invalid_credentials", "INVALID_CREDENTIALS"},
		{401, "unauthenticated", errorx.ErrUnauthorized},
		{429, "rate_limited", errorx.ErrHandlerTooManyRequests},
		{423, "account_locked", errorx.ErrHandlerTooManyRequests},
		{500, "internal", errorx.ErrInternal},
		{404, "role_not_found", errorx.ErrNotFound},
		{409, "email_taken", errorx.ErrConflict},
		{403, "missing_permission", errorx.ErrForbidden},
		{413, "body_too_large", errorx.ErrBadRequest},
		{400, "invalid_body", errorx.ErrBadRequest},
		{400, "weak_password", errorx.ErrValidation},
		{502, "upstream", errorx.ErrInternal},
	}
	for _, c := range cases {
		e := guardProblem(c.status, c.code, "msg")
		if e.Type != c.wantType || e.HTTPStatus != c.status {
			t.Errorf("%d %s: type %s status %d, want %s", c.status, c.code, e.Type, e.HTTPStatus, c.wantType)
		}
		_, hasReason := e.Extensions["reason"]
		if hasReason == (c.code == "internal") {
			t.Errorf("%s: reason extension present = %v", c.code, hasReason)
		}
	}
	if e := guardProblem(500, "internal", "stack trace here"); e.Details != "" {
		t.Errorf("internal message leaked: %q", e.Details)
	}
}
