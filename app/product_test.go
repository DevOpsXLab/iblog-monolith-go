package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/config"
)

func TestAnalyticsFunnelAndRetention(t *testing.T) {
	e := start(t, func(c *config.Config) { c.Analytics = true })
	anon := e.anon(t)

	var cfg struct {
		CaptchaSiteKey string `json:"captcha_site_key"`
		Analytics      bool   `json:"analytics"`
	}
	anon.do("GET", "/api/config", nil, 200, &cfg)
	if !cfg.Analytics || cfg.CaptchaSiteKey != "" {
		t.Fatalf("config = %+v", cfg)
	}

	// Two anonymous visitors; one opens the signup form.
	for _, v := range []string{"visitor-aaaa", "visitor-bbbb"} {
		anon.do("POST", "/api/events", map[string]any{"name": "page_view", "visitor": v, "path": "/", "lang": "uz",
			"referrer": "https://t.me/some/secret?token=x"}, 204, nil)
	}
	anon.do("POST", "/api/events", map[string]any{"name": "signup_open", "visitor": "visitor-aaaa"}, 204, nil)

	// Clients can't fake server-side steps, nor send junk.
	anon.do("POST", "/api/events", map[string]any{"name": "signup", "visitor": "visitor-aaaa"}, 400, nil)
	anon.do("POST", "/api/events", map[string]any{"name": "page_view", "visitor": "x"}, 400, nil)
	anon.do("POST", "/api/events", map[string]any{"name": "page_view", "visitor": "visitor-aaaa", "path": "http://evil"}, 400, nil)
	anon.do("POST", "/api/events", map[string]any{"name": "read_time", "visitor": "visitor-aaaa", "value": 30}, 400, nil)

	// Signup and first story are recorded by the server.
	ali := anon.register("ali")
	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Hello", "body": "world"}, 201, &p)
	ali.do("POST", "/api/events", map[string]any{"name": "read_time", "visitor": "visitor-aaaa", "post_id": p.ID, "value": 99999}, 204, nil)
	// A deleted or unknown post is dropped, not a 500.
	ali.do("POST", "/api/events", map[string]any{"name": "read_time", "visitor": "visitor-aaaa", "post_id": 987654, "value": 10}, 204, nil)

	// Only admins see the report.
	ali.do("GET", "/api/admin/analytics", nil, 403, nil)
	admin := anon.login("admin", "admin-pass-123")
	var rep struct {
		Days int `json:"days"`
		DAU  []struct {
			Day   string
			Count int
		} `json:"dau"`
		MAU    int `json:"mau"`
		Funnel []struct {
			Step  string `json:"step"`
			Count int    `json:"count"`
		} `json:"funnel"`
		AvgReadSeconds float64 `json:"avg_read_seconds"`
		Retention      struct {
			Cohort int `json:"cohort"`
		} `json:"retention"`
		Languages []struct {
			Key   string `json:"key"`
			Count int    `json:"count"`
		} `json:"languages"`
	}
	admin.do("GET", "/api/admin/analytics?days=7", nil, 200, &rep)
	if rep.Days != 7 || len(rep.DAU) != 7 || rep.DAU[6].Count < 3 || rep.MAU < 3 {
		t.Errorf("dau/mau = %+v %d", rep.DAU, rep.MAU)
	}
	want := map[string]int{"visit": 2, "signup_open": 1, "signup": 1, "first_publish": 1}
	for _, s := range rep.Funnel {
		if want[s.Step] != s.Count {
			t.Errorf("funnel %s = %d, want %d", s.Step, s.Count, want[s.Step])
		}
	}
	if rep.AvgReadSeconds > 3600 || rep.AvgReadSeconds <= 0 {
		t.Errorf("avg read = %v (capped at 3600)", rep.AvgReadSeconds)
	}
	if rep.Retention.Cohort < 1 {
		t.Errorf("cohort = %d", rep.Retention.Cohort)
	}
	if len(rep.Languages) == 0 || rep.Languages[0].Key != "uz" {
		t.Errorf("languages = %+v", rep.Languages)
	}
}

func TestPrerenderForCrawlers(t *testing.T) {
	e := start(t)
	ali := e.anon(t).register("ali")
	var p post
	ali.do("POST", "/api/posts", map[string]any{
		"title": `Kubernetes <script>alert(1)</script>`, "subtitle": "Deploy safely", "body": "## Intro\n\nHello **world**",
		"tags": []string{"devops"},
	}, 201, &p)

	get := func(path string) (int, string) {
		res, err := http.Get(e.srv.URL + path)
		must(t, err)
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	for _, path := range []string{"/api/prerender/@ali/" + p.Slug, "/api/prerender/p/" + p.Slug} {
		code, body := get(path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		for _, s := range []string{
			`<meta property="og:type" content="article">`,
			`<meta name="description" content="Deploy safely">`,
			`<link rel="canonical" href="http://site.test/@ali/` + p.Slug + `">`,
			`<meta property="article:tag" content="devops">`,
			`"@type":"Article"`,
			`<h2`, // body rendered
		} {
			if !strings.Contains(body, s) {
				t.Errorf("%s: missing %s in\n%s", path, s, body)
			}
		}
		if strings.Contains(body, "<script>alert") {
			t.Errorf("%s: title not escaped", path)
		}
	}

	if code, body := get("/api/prerender/@ali"); code != 200 || !strings.Contains(body, `content="profile"`) {
		t.Errorf("profile = %d %s", code, body)
	}
	if code, body := get("/api/prerender/"); code != 200 || !strings.Contains(body, "iBlog") && !strings.Contains(body, "<title>") {
		t.Errorf("home = %d %s", code, body)
	}
	if code, _ := get("/api/prerender/p/no-such-post"); code != 404 {
		t.Errorf("missing post = %d", code)
	}
}

func TestSignupCaptchaAndHoneypot(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("response") == "human" {
			w.Write([]byte(`{"success":true}`))
			return
		}
		w.Write([]byte(`{"success":false}`))
	}))
	defer cf.Close()
	e := start(t, func(c *config.Config) {
		c.TurnstileSiteKey, c.TurnstileSecret, c.TurnstileVerifyURL = "site-key", "secret", cf.URL
	})
	anon := e.anon(t)

	var cfg struct {
		CaptchaSiteKey string `json:"captcha_site_key"`
	}
	anon.do("GET", "/api/config", nil, 200, &cfg)
	if cfg.CaptchaSiteKey != "site-key" {
		t.Fatalf("site key = %q", cfg.CaptchaSiteKey)
	}
	body := func(token, website string) map[string]string {
		return map[string]string{"username": "bob", "email": "bob@example.com", "password": "secret123",
			"captcha_token": token, "website": website}
	}
	anon.do("POST", "/api/auth/register", body("", ""), 400, nil)
	anon.do("POST", "/api/auth/register", body("robot", ""), 400, nil)
	anon.do("POST", "/api/auth/register", body("human", "http://spam"), 400, nil)
	anon.do("POST", "/api/auth/register", body("human", ""), 201, nil)
}
