package app_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// raw sends a request and returns the status, headers and decoded body
// without the test client's unwrapping.
func (c *client) raw(method, path, body string, header map[string]string) (int, http.Header, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	must(c.t, err)
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			c.t.Fatalf("%s %s: not JSON: %s", method, path, data)
		}
	}
	return res.StatusCode, res.Header, out
}

func TestResponseFormat(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	for i := range 3 {
		ali.do("POST", "/api/posts", map[string]any{"title": fmt.Sprintf("Post %d", i)}, 201, nil)
	}

	// An object comes as {"data": {...}}.
	code, h, body := ali.raw("GET", "/api/me", "", nil)
	data, _ := body["data"].(map[string]any)
	if code != 200 || data["username"] != "ali" || body["meta"] != nil || h.Get("X-Request-ID") == "" {
		t.Errorf("object: %d %v %v", code, body, h)
	}

	// A page comes as {"data": [...], "meta": {...}}.
	code, _, body = anon.raw("GET", "/api/posts?limit=2", "", nil)
	items, _ := body["data"].([]any)
	meta, _ := body["meta"].(map[string]any)
	if code != 200 || len(items) != 2 || meta["total"] != 3.0 || meta["limit"] != 2.0 || meta["page"] != 1.0 ||
		meta["has_more"] != true || meta["next_cursor"] == "" {
		t.Errorf("page: %v", body)
	}
	_, _, body = anon.raw("GET", "/api/posts?limit=2&cursor="+meta["next_cursor"].(string), "", nil)
	if meta := body["meta"].(map[string]any); meta["has_more"] != false || len(body["data"].([]any)) != 1 {
		t.Errorf("last page: %v", body)
	}
	// Empty lists are [], never null; small lists have no meta.
	_, _, body = anon.raw("GET", "/api/categories", "", nil)
	if l, ok := body["data"].([]any); !ok || len(l) != 0 || body["meta"] != nil {
		t.Errorf("empty list: %v", body)
	}

	// Errors are RFC 9457 problem details with stable numeric codes,
	// localized titles and the request id.
	problems := []struct {
		c       *client
		method  string
		path    string
		body    string
		status  int
		code    string
		numeric float64
		detail  string
	}{
		{anon, "GET", "/api/posts/999", "", 404, "NOT_FOUND", 1012, "post not found"},
		{anon, "GET", "/api/posts/abc", "", 400, "BAD_REQUEST", 1001, "bad id"},
		{anon, "GET", "/api/nope", "", 404, "NOT_FOUND", 1012, "no such endpoint"},
		{anon, "GET", "/api/me", "", 401, "UNAUTHORIZED", 1004, "unauthorized"},
		{ali, "POST", "/api/posts", `{"title": " "}`, 400, "VALIDATION_ERROR", 1003, "title required"},
		{ali, "POST", "/api/posts", `{`, 400, "BAD_REQUEST", 1001, "invalid json"},
		{ali, "POST", "/api/categories", `{"name": "x"}`, 403, "FORBIDDEN", 1008, "forbidden"},
		{anon, "POST", "/api/auth/login", `{"login": "ali", "password": "wrong-pass"}`, 401, "INVALID_CREDENTIALS", 6001, ""},
	}
	for _, p := range problems {
		code, h, body := p.c.raw(p.method, p.path, p.body, map[string]string{"Accept-Language": "uz", "X-Request-ID": "req-123"})
		if code != p.status || body["status"] != float64(p.status) || body["code"] != p.code || body["numeric_code"] != p.numeric ||
			(p.detail != "" && body["detail"] != p.detail) || body["title"] == "" || body["instance"] != strings.Split(p.path, "?")[0] ||
			body["request_id"] != "req-123" || h.Get("X-Request-ID") != "req-123" ||
			!strings.HasPrefix(h.Get("Content-Type"), "application/problem+json") {
			t.Errorf("%s %s = %d %v %v", p.method, p.path, code, body, h.Get("Content-Type"))
		}
	}
	_, _, body = anon.raw("GET", "/api/posts/999", "", map[string]string{"Accept-Language": "uz"})
	if body["title"] != "So'ralgan resurs topilmadi." {
		t.Errorf("uz title = %v", body["title"])
	}

	// 204 has no body.
	code, _, body = ali.raw("POST", "/api/tags/go/follow", "", nil)
	if code != 204 || body != nil {
		t.Errorf("204: %d %v", code, body)
	}

	// 429 is retryable with Retry-After.
	var last int
	var lastBody map[string]any
	var lastH http.Header
	for range 12 {
		last, lastH, lastBody = anon.raw("POST", "/api/auth/register", `{}`, nil)
		if last == 429 {
			break
		}
	}
	if last != 429 || lastBody["numeric_code"] != 4029.0 || lastBody["retryable"] != true || lastH.Get("Retry-After") == "" {
		t.Errorf("429: %d %v %v", last, lastBody, lastH)
	}

	// Guard's routes and its authentication speak the same format.
	admin := anon.login("admin", "admin-pass-123")
	code, _, body = admin.raw("GET", "/api/guard/roles", "", nil)
	if roles, ok := body["data"].([]any); code != 200 || !ok || len(roles) == 0 {
		t.Errorf("guard list: %d %v", code, body)
	}
	code, _, body = admin.raw("GET", "/api/guard/auth/me", "", nil)
	if d, ok := body["data"].(map[string]any); code != 200 || !ok || d["user"] == nil {
		t.Errorf("guard object: %d %v", code, body)
	}
	code, h, body = admin.raw("DELETE", "/api/guard/roles/no-such-role", "", map[string]string{"X-Request-ID": "g-1"})
	if code != 404 || body["code"] != "NOT_FOUND" || body["numeric_code"] != 1012.0 || body["reason"] != "role_not_found" ||
		body["request_id"] != "g-1" || !strings.HasPrefix(h.Get("Content-Type"), "application/problem+json") {
		t.Errorf("guard error: %d %v", code, body)
	}
	bad := &client{t: t, base: anon.base, token: "not-a-token"}
	code, _, body = bad.raw("GET", "/api/me", "", nil)
	if code != 401 || body["code"] != "UNAUTHORIZED" {
		t.Errorf("bad token: %d %v", code, body)
	}
	code, _, body = bad.raw("GET", "/api/guard/roles", "", nil)
	if code != 401 || body["code"] != "UNAUTHORIZED" || body["reason"] != "unauthenticated" {
		t.Errorf("guard bad token: %d %v", code, body)
	}

	// Health is data when up.
	code, _, body = anon.raw("GET", "/api/healthz", "", nil)
	if d, _ := body["data"].(map[string]any); code != 200 || d["postgres"] != "ok" {
		t.Errorf("healthz: %d %v", code, body)
	}

	// Server-sent events carry the same {"data": ...} envelope.
	req, _ := http.NewRequest("GET", anon.base+"/api/me/notifications/stream", nil)
	req.Header.Set("Authorization", "Bearer "+ali.token)
	res, err := http.DefaultClient.Do(req)
	must(t, err)
	defer res.Body.Close()
	lines := bufio.NewScanner(res.Body)
	lines.Scan() // ": connected"
	vali.do("POST", "/api/users/ali/follow", nil, 204, nil)
	for lines.Scan() {
		line, ok := strings.CutPrefix(lines.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct{ Data struct{ Type string } }
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Data.Type != "follow" {
			t.Errorf("event = %s", line)
		}
		break
	}
}
