package app_test

// End-to-end tests: the whole app against real Postgres, Redis and MinIO in
// containers (testcontainers). Skipped with -short or without Docker.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/app"
	"github.com/iBlog/iblog-monolith-go/config"
	"github.com/gen2brain/webp"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

type env struct {
	srv *httptest.Server
	app *app.App
}

// start runs the app against fresh containers; opts adjust the config.
func start(t *testing.T, opts ...func(*config.Config)) *env {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("blog"), tcpostgres.WithUsername("blog"), tcpostgres.WithPassword("blog"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	must(t, err)

	rd, err := tcredis.Run(ctx, "redis:8-alpine")
	must(t, err)
	t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
	redisURL, err := rd.ConnectionString(ctx)
	must(t, err)

	mn, err := tcminio.Run(ctx, "cgr.dev/chainguard/minio:latest", tcminio.WithUsername("minio"), tcminio.WithPassword("minio12345"))
	must(t, err)
	t.Cleanup(func() { testcontainers.TerminateContainer(mn) })
	s3, err := mn.ConnectionString(ctx)
	must(t, err)

	cfg := config.Config{
		Env:            "dev",
		MFAKey:         config.DevMFAKey,
		UnsubscribeKey: config.DevUnsubscribeKey,
		MigrateOnStart: true,
		DBMaxConns:     10,
		DatabaseURL:    dsn,
		RedisURL:       redisURL,
		AdminUsername:  "admin",
		AdminEmail:     "admin@iblog.dev",
		AdminPassword:  "admin-pass-123",
		S3Endpoint:     s3,
		S3AccessKey:    "minio",
		S3SecretKey:    "minio12345",
		S3Bucket:       "uploads",
		SiteURL:        "http://site.test",
		Worker:         true,
		DocsDir:        "..",
		PublicURL:      "http://localhost",
		// Cheapest argon2id Guard accepts keeps the suite fast.
		PasswordHashMemoryKiB: 19456,
		PasswordHashTime:      2,
		PasswordHashThreads:   1,
		AuditEmailKey:         "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
	}
	for _, o := range opts {
		o(&cfg)
	}
	a, err := app.New(ctx, cfg)
	must(t, err)
	must(t, a.Start())
	t.Cleanup(a.Close)
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return &env{srv: srv, app: a}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// eventually polls cond for up to 15 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

type client struct {
	t     *testing.T
	base  string
	token string
	user  struct{ ID int }
}

func (e *env) anon(t *testing.T) *client { return &client{t: t, base: e.srv.URL} }

// do sends JSON, checks the status and decodes the response into out.
func (c *client) do(method, path string, body any, want int, out any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	c.send(req, want, out)
}

func (c *client) status(method, path string, body any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	must(c.t, err)
	res.Body.Close()
	return res.StatusCode
}

func (c *client) send(req *http.Request, want int, out any) {
	c.t.Helper()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	must(c.t, err)
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s = %d, want %d: %s", req.Method, req.URL.Path, res.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(unwrap(c.t, data), out); err != nil {
			c.t.Fatalf("decode %s: %v", data, err)
		}
	}
}

// unwrap turns the API's envelopes back into the flat shapes the tests
// decode: {"data": x} → x; a page {"data": [...], "meta": {...}} → meta's
// fields plus "items"; a problem → {"error": detail, "code", "numeric_code"}.
// TestResponseFormat checks the envelopes themselves.
func unwrap(t *testing.T, data []byte) []byte {
	t.Helper()
	var env struct {
		Data        json.RawMessage `json:"data"`
		Meta        map[string]any  `json:"meta"`
		Title       string          `json:"title"`
		Detail      string          `json:"detail"`
		Code        string          `json:"code"`
		NumericCode int             `json:"numeric_code"`
	}
	if json.Unmarshal(data, &env) != nil {
		return data
	}
	switch {
	case env.Code != "" && env.Title != "":
		msg := env.Detail
		if msg == "" {
			msg = env.Title
		}
		out, _ := json.Marshal(map[string]any{"error": msg, "code": env.Code, "numeric_code": env.NumericCode})
		return out
	case env.Meta != nil:
		flat := env.Meta
		flat["items"] = env.Data
		out, _ := json.Marshal(flat)
		return out
	case env.Data != nil:
		return env.Data
	}
	return data
}

type session struct {
	Token string `json:"token"`
	User  struct {
		ID    int      `json:"id"`
		Roles []string `json:"roles"`
	} `json:"user"`
}

func (c *client) as(s session) *client {
	n := &client{t: c.t, base: c.base, token: s.Token}
	n.user.ID = s.User.ID
	return n
}

func (c *client) register(name string) *client {
	c.t.Helper()
	var s session
	c.do("POST", "/api/auth/register", map[string]string{
		"username": name, "email": name + "@example.com", "password": "secret123",
	}, 201, &s)
	return c.as(s)
}

func (c *client) login(login, password string) *client {
	c.t.Helper()
	var s session
	c.do("POST", "/api/auth/login", map[string]string{"login": login, "password": password}, 200, &s)
	return c.as(s)
}

type post struct {
	ID            int    `json:"id"`
	Slug          string `json:"slug"`
	Status        string `json:"status"`
	Author        string `json:"author"`
	Likes         int    `json:"likes"`
	Claps         int    `json:"claps"`
	MyClaps       int    `json:"my_claps"`
	Liked         bool   `json:"liked"`
	Bookmarked    bool   `json:"bookmarked"`
	CommentsCount int    `json:"comments_count"`
	Views         int64  `json:"views"`
	Labels        []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type page struct {
	Items      []post `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"next_cursor"`
}

func TestRBACAndABAC(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")
	vali := anon.register("vali")
	mod := anon.register("moddy")

	// RBAC: categories, labels and stats need admin permissions.
	ali.do("POST", "/api/categories", map[string]string{"name": "Go"}, 403, nil)
	var cat, label struct{ ID int }
	admin.do("POST", "/api/categories", map[string]string{"name": "Go"}, 201, &cat)
	admin.do("POST", "/api/labels", map[string]string{"name": "Guide", "color": "#22aa66"}, 201, &label)
	ali.do("GET", "/api/admin/stats", nil, 403, nil)
	admin.do("GET", "/api/admin/stats", nil, 200, nil)

	// Anonymous cannot write.
	anon.do("POST", "/api/posts", map[string]any{"title": "x"}, 401, nil)

	var p post
	ali.do("POST", "/api/posts", map[string]any{
		"title": "Kubernetes tips", "body": "# Hello\nDeploying **pods**", "category_id": cat.ID, "label_ids": []int{label.ID},
	}, 201, &p)
	if p.Author != "ali" || len(p.Labels) != 1 || p.Labels[0].Name != "Guide" {
		t.Errorf("post = %+v", p)
	}
	ali.do("POST", "/api/posts", map[string]any{"title": "x", "label_ids": []int{999}}, 400, nil)

	// ABAC: the author may edit; another user may not.
	vali.do("PUT", fmt.Sprintf("/api/posts/%d", p.ID), map[string]any{"title": "hacked"}, 403, nil)
	ali.do("PUT", fmt.Sprintf("/api/posts/%d", p.ID), map[string]any{"title": "Kubernetes tips v2", "body": "pods", "label_ids": []int{label.ID}}, 200, nil)

	// RBAC: a moderator (role granted through Guard's admin API) may delete anyone's comment.
	var c struct{ ID int }
	vali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "first"}, 201, &c)
	mod.do("DELETE", fmt.Sprintf("/api/comments/%d", c.ID), nil, 403, nil)
	admin.do("POST", fmt.Sprintf("/api/guard/users/%d/roles", mod.user.ID), map[string]string{"role": "moderator"}, 204, nil)
	var me struct{ Roles []string }
	mod.do("GET", "/api/me", nil, 200, &me)
	if !strings.Contains(strings.Join(me.Roles, ","), "moderator") {
		t.Fatalf("roles = %v", me.Roles)
	}
	mod.do("DELETE", fmt.Sprintf("/api/comments/%d", c.ID), nil, 204, nil)
	mod.do("GET", "/api/admin/comments", nil, 200, nil)
	ali.do("GET", "/api/admin/comments", nil, 403, nil)

	// Ban through Guard: every session ends.
	admin.do("PUT", fmt.Sprintf("/api/guard/users/%d/status", vali.user.ID), map[string]string{"status": "banned"}, 204, nil)
	vali.do("GET", "/api/me", nil, 401, nil)
	anon.do("POST", "/api/auth/login", map[string]string{"login": "vali", "password": "secret123"}, 401, nil)

	// Audit trail has blog and security events.
	var audit []struct{ Action string }
	admin.do("GET", "/api/guard/audit?limit=200", nil, 200, &audit)
	got := map[string]bool{}
	for _, ev := range audit {
		got[ev.Action] = true
	}
	for _, want := range []string{"category.create", "label.create", "comment.delete", "role.assign", "user.status", "auth.login"} {
		if !got[want] {
			t.Errorf("audit lacks %q", want)
		}
	}
}

func TestPostsCommentsAndSocial(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	// Follow, then publishing notifies followers (queue fan-out).
	vali.do("POST", "/api/users/ali/follow", nil, 204, nil)
	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Hello world", "body": "first post", "tags": []string{"Go"}}, 201, &p)
	var notes struct {
		Items  []struct{ Type string } `json:"items"`
		Unread int                     `json:"unread"`
	}
	eventually(t, "new_post notification", func() bool {
		vali.do("GET", "/api/me/notifications", nil, 200, &notes)
		return len(notes.Items) == 1 && notes.Items[0].Type == "new_post"
	})
	var feed page
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 1 {
		t.Errorf("feed total = %d", feed.Total)
	}
	var follow struct{ Items []struct{ Type string } }
	ali.do("GET", "/api/me/notifications", nil, 200, &follow)
	if len(follow.Items) != 1 || follow.Items[0].Type != "follow" {
		t.Errorf("follow notification = %+v", follow)
	}

	// Slug, likes (once per user), bookmarks, views.
	var bySlug post
	anon.do("GET", "/api/slug/"+p.Slug, nil, 200, &bySlug)
	if bySlug.ID != p.ID {
		t.Fatal("slug lookup")
	}
	vali.do("POST", fmt.Sprintf("/api/posts/%d/like", p.ID), nil, 200, nil)
	vali.do("POST", fmt.Sprintf("/api/posts/%d/like", p.ID), nil, 200, &p)
	if p.Likes != 1 || !p.Liked {
		t.Errorf("likes = %d liked = %v", p.Likes, p.Liked)
	}
	vali.do("POST", fmt.Sprintf("/api/posts/%d/bookmark", p.ID), nil, 204, nil)
	var bm page
	vali.do("GET", "/api/me/bookmarks", nil, 200, &bm)
	if bm.Total != 1 {
		t.Errorf("bookmarks = %d", bm.Total)
	}
	for range 3 {
		anon.do("POST", fmt.Sprintf("/api/posts/%d/view", p.ID), nil, 204, nil) // same IP: one view
	}
	vali.do("POST", fmt.Sprintf("/api/posts/%d/view", p.ID), nil, 204, nil)
	vali.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 200, &p)
	if p.Views != 2 || !p.Bookmarked {
		t.Errorf("views = %d bookmarked = %v", p.Views, p.Bookmarked)
	}
	var trending []post
	anon.do("GET", "/api/posts/trending", nil, 200, &trending)
	if len(trending) != 1 || trending[0].ID != p.ID {
		t.Errorf("trending = %+v", trending)
	}

	// Comments: reply, edit by author only, pagination, count.
	var c1, c2 struct {
		ID       int    `json:"id"`
		ParentID int    `json:"parent_id"`
		EditedAt string `json:"edited_at"`
	}
	vali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]any{"text": "nice"}, 201, &c1)
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]any{"text": "thanks", "parent_id": c1.ID}, 201, &c2)
	if c2.ParentID != c1.ID {
		t.Errorf("reply parent = %d", c2.ParentID)
	}
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]any{"text": "x", "parent_id": 9999}, 400, nil)
	ali.do("PUT", fmt.Sprintf("/api/comments/%d", c1.ID), map[string]string{"text": "edited"}, 403, nil)
	vali.do("PUT", fmt.Sprintf("/api/comments/%d", c1.ID), map[string]string{"text": "very nice"}, 200, &c1)
	if c1.EditedAt == "" {
		t.Error("edited_at not set")
	}
	var cp struct {
		Items []struct{ ID int } `json:"items"`
		Total int                `json:"total"`
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d/comments?limit=1", p.ID), nil, 200, &cp)
	if cp.Total != 2 || len(cp.Items) != 1 {
		t.Errorf("comment page = %+v", cp)
	}
	vali.do("GET", "/api/me/notifications", nil, 200, &notes)
	if notes.Items[0].Type != "reply" {
		t.Errorf("latest notification = %+v", notes.Items[0])
	}
	vali.do("POST", "/api/me/notifications/read", map[string]any{}, 204, nil)
	vali.do("GET", "/api/me/notifications", nil, 200, &notes)
	if notes.Unread == 0 {
		t.Errorf("empty ids marked notifications read")
	}
	vali.do("POST", "/api/me/notifications/read", map[string]any{"all": true}, 204, nil)
	vali.do("GET", "/api/me/notifications", nil, 200, &notes)
	if notes.Unread != 0 {
		t.Errorf("unread = %d", notes.Unread)
	}

	// RSS and sitemap list the post by slug.
	for _, path := range []string{"/api/feed.xml", "/api/sitemap.xml"} {
		res, err := http.Get(e.srv.URL + path)
		must(t, err)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(body), "/"+p.Slug+"<") || !strings.Contains(string(body), "http://site.test/@") {
			t.Errorf("%s: %d %s", path, res.StatusCode, body)
		}
	}
}

func TestClapsHighlightsAndAuthorStats(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	gani := anon.register("gani")

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Claps", "body": "Ünicode first. Second sentence here."}, 201, &p)
	clap := func(c *client, n, want int) {
		c.do("POST", fmt.Sprintf("/api/posts/%d/clap", p.ID), map[string]int{"count": n}, want, &p)
	}

	// Claps: up to 50 per reader; likes counts clappers.
	clap(vali, 0, 400)
	clap(vali, 51, 400)
	anon.do("POST", fmt.Sprintf("/api/posts/%d/clap", p.ID), map[string]int{"count": 1}, 401, nil)
	clap(vali, 30, 200)
	clap(vali, 30, 200)
	if p.MyClaps != 50 || p.Claps != 50 || p.Likes != 1 || !p.Liked {
		t.Errorf("after 30+30: my=%d claps=%d likes=%d liked=%v", p.MyClaps, p.Claps, p.Likes, p.Liked)
	}
	clap(gani, 5, 200)
	gani.do("POST", fmt.Sprintf("/api/posts/%d/like", p.ID), nil, 200, &p) // already clapped: no-op
	if p.Claps != 55 || p.Likes != 2 || p.MyClaps != 5 {
		t.Errorf("after gani: claps=%d likes=%d my=%d", p.Claps, p.Likes, p.MyClaps)
	}
	vali.do("DELETE", fmt.Sprintf("/api/posts/%d/like", p.ID), nil, 200, &p)
	if p.Claps != 5 || p.Likes != 1 || p.MyClaps != 0 {
		t.Errorf("after unclap: claps=%d likes=%d my=%d", p.Claps, p.Likes, p.MyClaps)
	}
	var notes struct{ Items []struct{ Type string } }
	ali.do("GET", "/api/me/notifications", nil, 200, &notes)
	if len(notes.Items) != 2 { // vali's and gani's first claps only
		t.Errorf("clap notifications = %d", len(notes.Items))
	}

	// Highlights: offsets are runes; text comes from the body.
	type hl struct {
		ID   int    `json:"id"`
		Text string `json:"text"`
	}
	var h1, h2 hl
	path := fmt.Sprintf("/api/posts/%d/highlights", p.ID)
	vali.do("POST", path, map[string]int{"start": 0, "end": 14}, 201, &h1)
	if h1.Text != "Ünicode first." {
		t.Errorf("highlight text = %q", h1.Text)
	}
	gani.do("POST", path, map[string]int{"start": 0, "end": 14}, 201, &h2)
	vali.do("POST", path, map[string]int{"start": 5, "end": 999}, 400, nil)
	anon.do("POST", path, map[string]int{"start": 0, "end": 3}, 401, nil)
	var hs struct {
		Top []struct {
			Text  string
			Count int
		} `json:"top"`
		Mine []hl `json:"mine"`
	}
	vali.do("GET", path, nil, 200, &hs)
	if len(hs.Top) != 1 || hs.Top[0].Count != 2 || len(hs.Mine) != 1 {
		t.Errorf("highlights = %+v", hs)
	}
	gani.do("DELETE", fmt.Sprintf("/api/highlights/%d", h1.ID), nil, 403, nil)
	vali.do("DELETE", fmt.Sprintf("/api/highlights/%d", h1.ID), nil, 204, nil)

	// Author stats.
	anon.do("GET", "/api/me/stats", nil, 401, nil)
	vali.do("POST", fmt.Sprintf("/api/posts/%d/view", p.ID), nil, 204, nil)
	var st struct {
		Items []struct {
			PostID     int   `json:"post_id"`
			Views      int64 `json:"views"`
			Claps      int   `json:"claps"`
			Highlights int   `json:"highlights"`
		} `json:"items"`
		Totals struct{ Claps int } `json:"totals"`
	}
	ali.do("GET", "/api/me/stats", nil, 200, &st)
	if len(st.Items) != 1 || st.Items[0].Views != 1 || st.Items[0].Claps != 5 || st.Items[0].Highlights != 1 || st.Totals.Claps != 5 {
		t.Errorf("stats = %+v", st)
	}
}

func TestReportsModeration(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")
	vali := anon.register("vali")
	gani := anon.register("gani")
	mod := anon.register("moddy")
	admin.do("POST", fmt.Sprintf("/api/guard/users/%d/roles", mod.user.ID), map[string]string{"role": "moderator"}, 204, nil)

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Buy now", "body": "spam"}, 201, &p)
	var c struct{ ID int }
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "more spam"}, 201, &c)

	type rep struct {
		ID      int    `json:"id"`
		Status  string `json:"status"`
		Reports int    `json:"reports"`
	}
	report := func(cl *client, typ string, id int, reason string, want int) rep {
		var r rep
		cl.do("POST", "/api/reports", map[string]any{"target_type": typ, "target_id": id, "reason": reason}, want, &r)
		return r
	}
	anon.do("POST", "/api/reports", map[string]any{"target_type": "post", "target_id": p.ID, "reason": "spam"}, 401, nil)
	report(vali, "post", p.ID, "nope", 400)
	report(vali, "post", p.ID, "other", 400) // note required
	report(ali, "post", p.ID, "spam", 400)   // own content
	report(vali, "post", 9999, "spam", 404)
	r1 := report(vali, "post", p.ID, "spam", 201)
	report(vali, "post", p.ID, "spam", 409) // one open report per target
	r2 := report(gani, "post", p.ID, "abuse", 201)
	if r2.Reports != 2 {
		t.Errorf("open reports on target = %d", r2.Reports)
	}
	rc := report(vali, "comment", c.ID, "spam", 201)
	report(vali, "user", ali.user.ID, "harassment", 201)

	// Only moderators list and decide.
	vali.do("GET", "/api/admin/reports", nil, 403, nil)
	var pg struct{ Total int }
	mod.do("GET", "/api/admin/reports?status=open", nil, 200, &pg)
	if pg.Total != 4 {
		t.Errorf("open reports = %d", pg.Total)
	}
	vali.do("PUT", fmt.Sprintf("/api/admin/reports/%d", r1.ID), map[string]any{"status": "resolved"}, 403, nil)
	mod.do("PUT", fmt.Sprintf("/api/admin/reports/%d", r1.ID), map[string]any{"status": "open"}, 400, nil)
	mod.do("PUT", fmt.Sprintf("/api/admin/reports/%d", r1.ID), map[string]any{"status": "dismissed", "remove_content": true}, 400, nil)

	// Resolving with removal deletes the post and closes duplicate reports.
	var done rep
	mod.do("PUT", fmt.Sprintf("/api/admin/reports/%d", r1.ID), map[string]any{"status": "resolved", "remove_content": true}, 200, &done)
	if done.Status != "resolved" {
		t.Errorf("status = %q", done.Status)
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 404, nil)
	mod.do("PUT", fmt.Sprintf("/api/admin/reports/%d", r2.ID), map[string]any{"status": "resolved"}, 409, nil)
	mod.do("PUT", fmt.Sprintf("/api/admin/reports/%d", rc.ID), map[string]any{"status": "dismissed"}, 200, nil)
	mod.do("GET", "/api/admin/reports?status=open", nil, 200, &pg)
	if pg.Total != 1 {
		t.Errorf("open after decisions = %d", pg.Total)
	}
	vali.do("POST", "/api/reports", map[string]any{"target_type": "post", "target_id": p.ID, "reason": "spam"}, 404, nil)
}

func TestPublications(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")   // owner
	vali := anon.register("vali") // editor
	gani := anon.register("gani") // writer
	out := anon.register("outsider")

	type pub struct {
		ID      int    `json:"id"`
		Slug    string `json:"slug"`
		Name    string `json:"name"`
		Members int    `json:"members"`
		Posts   int    `json:"posts"`
		MyRole  string `json:"my_role"`
	}
	anon.do("POST", "/api/publications", map[string]string{"slug": "go-team", "name": "Go"}, 401, nil)
	ali.do("POST", "/api/publications", map[string]string{"slug": "Bad Slug!", "name": "Go"}, 400, nil)
	var pb pub
	ali.do("POST", "/api/publications", map[string]string{"slug": "go-team", "name": "Go Team"}, 201, &pb)
	if pb.MyRole != "owner" || pb.Members != 1 {
		t.Errorf("created = %+v", pb)
	}
	vali.do("POST", "/api/publications", map[string]string{"slug": "go-team", "name": "x"}, 409, nil)

	// Members: owner adds editor; editor adds writers but not editors.
	base := "/api/publications/go-team/members/"
	vali.do("PUT", base+"gani", map[string]string{"role": "writer"}, 403, nil)
	ali.do("PUT", base+"vali", map[string]string{"role": "editor"}, 204, nil)
	vali.do("PUT", base+"gani", map[string]string{"role": "writer"}, 204, nil)
	vali.do("PUT", base+"gani", map[string]string{"role": "editor"}, 403, nil)
	vali.do("PUT", base+"ali", map[string]string{"role": "writer"}, 403, nil)
	ali.do("PUT", base+"gani", map[string]string{"role": "owner"}, 400, nil)
	ali.do("PUT", base+"nobody", map[string]string{"role": "writer"}, 404, nil)
	var members []struct{ Username, Role string }
	anon.do("GET", "/api/publications/go-team/members", nil, 200, &members)
	if len(members) != 3 || members[0].Role != "owner" || members[1].Role != "editor" {
		t.Errorf("members = %+v", members)
	}

	// Writing into a publication needs membership.
	out.do("POST", "/api/posts", map[string]any{"title": "x", "publication_id": pb.ID}, 403, nil)
	var p post
	gani.do("POST", "/api/posts", map[string]any{"title": "Generics", "body": "hi", "publication_id": pb.ID}, 201, &p)
	var pp page
	anon.do("GET", "/api/publications/go-team/posts", nil, 200, &pp)
	if pp.Total != 1 {
		t.Errorf("publication posts = %d", pp.Total)
	}

	// Editors edit any post in the publication; others cannot.
	edit := map[string]any{"title": "Generics (edited)", "body": "hi", "publication_id": pb.ID}
	out.do("PUT", fmt.Sprintf("/api/posts/%d", p.ID), edit, 403, nil)
	vali.do("PUT", fmt.Sprintf("/api/posts/%d", p.ID), edit, 200, nil)
	out.do("PATCH", "/api/publications/go-team", map[string]string{"name": "Hacked"}, 403, nil)
	vali.do("PATCH", "/api/publications/go-team", map[string]string{"name": "Go Team!"}, 200, &pb)
	if pb.Name != "Go Team!" || pb.Posts != 1 {
		t.Errorf("updated = %+v", pb)
	}

	// Leaving and removal; the owner stays.
	var mine []pub
	gani.do("GET", "/api/me/publications", nil, 200, &mine)
	if len(mine) != 1 || mine[0].MyRole != "writer" {
		t.Errorf("my publications = %+v", mine)
	}
	gani.do("DELETE", base+"gani", nil, 204, nil)
	vali.do("DELETE", base+"ali", nil, 403, nil)
	ali.do("DELETE", base+"ali", nil, 403, nil)

	// Only the owner deletes; posts survive.
	vali.do("DELETE", "/api/publications/go-team", nil, 403, nil)
	ali.do("DELETE", "/api/publications/go-team", nil, 204, nil)
	anon.do("GET", "/api/publications/go-team", nil, 404, nil)
	anon.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 200, nil)
}

func TestKeysetPagination(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali, vali := anon.register("ali"), anon.register("vali") // 10 posts/min each
	for i := range 12 {
		author := ali
		if i%2 == 1 {
			author = vali
		}
		author.do("POST", "/api/posts", map[string]any{"title": fmt.Sprintf("Post %d", i)}, 201, nil)
	}

	seen := map[int]bool{}
	var pages []int
	path := "/api/posts?limit=5"
	for {
		var pg page
		anon.do("GET", path, nil, 200, &pg)
		pages = append(pages, len(pg.Items))
		for _, p := range pg.Items {
			if seen[p.ID] {
				t.Fatalf("post %d twice", p.ID)
			}
			seen[p.ID] = true
		}
		if pg.NextCursor == "" {
			break
		}
		// A post published mid-walk must not shift later pages.
		if len(pages) == 1 {
			anon.register("gani").do("POST", "/api/posts", map[string]any{"title": "Newer"}, 201, nil)
		}
		path = "/api/posts?limit=5&cursor=" + pg.NextCursor
	}
	if fmt.Sprint(pages) != "[5 5 2]" || len(seen) != 12 {
		t.Errorf("pages = %v, seen %d", pages, len(seen))
	}
	anon.do("GET", "/api/posts?cursor=garbage", nil, 400, nil)
	anon.do("GET", "/api/posts?q=post&cursor=MTox", nil, 400, nil)
}

func TestVerifyCommentLikesTagsRSSMarkdown(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	// Email verification: link emailed on sign-up, code works once.
	var me struct {
		EmailVerified bool `json:"email_verified"`
	}
	ali.do("GET", "/api/me", nil, 200, &me)
	if me.EmailVerified {
		t.Fatal("verified before confirming")
	}
	var code string
	eventually(t, "verification email", func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if i := strings.Index(m.Text, "code="); i >= 0 && m.To == "ali@example.com" {
				code = strings.Fields(m.Text[i+len("code="):])[0]
				return true
			}
		}
		return false
	})
	anon.do("POST", "/api/auth/verify-email", map[string]string{"code": "nope"}, 400, nil)
	anon.do("POST", "/api/auth/verify-email", map[string]string{"code": code}, 204, nil)
	anon.do("POST", "/api/auth/verify-email", map[string]string{"code": code}, 400, nil)
	ali.do("GET", "/api/me", nil, 200, &me)
	if !me.EmailVerified {
		t.Error("not verified")
	}
	ali.do("POST", "/api/me/verify-email", nil, 400, nil) // already verified
	vali.do("POST", "/api/me/verify-email", nil, 202, nil)

	// Markdown is rendered and sanitized on single-post reads only.
	var p struct {
		ID       int    `json:"id"`
		Slug     string `json:"slug"`
		Author   string `json:"author"`
		BodyHTML string `json:"body_html"`
	}
	body := "# Hi\n\n<script>alert(1)</script>\n\nhttps://youtu.be/dQw4w9WgXcQ"
	ali.do("POST", "/api/posts", map[string]any{"title": "Kube", "body": body, "tags": []string{"Kubernetes"}}, 201, nil)
	var list page
	anon.do("GET", "/api/posts", nil, 200, &list)
	p.ID = list.Items[0].ID
	anon.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 200, &p)
	if !strings.Contains(p.BodyHTML, `<h1 id="hi">`) || strings.Contains(p.BodyHTML, "<script") ||
		!strings.Contains(p.BodyHTML, "youtube-nocookie.com/embed/dQw4w9WgXcQ") {
		t.Errorf("body_html = %q", p.BodyHTML)
	}

	// Author follows renames: shown from users, not the stored copy.
	// (username is immutable through the API, so check it matches.)
	if p.Author != "ali" {
		t.Errorf("author = %q", p.Author)
	}

	// Comment likes.
	var c struct {
		ID    int  `json:"id"`
		Likes int  `json:"likes"`
		Liked bool `json:"liked"`
	}
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "first"}, 201, &c)
	anon.do("POST", fmt.Sprintf("/api/comments/%d/like", c.ID), nil, 401, nil)
	vali.do("POST", fmt.Sprintf("/api/comments/%d/like", c.ID), nil, 200, nil)
	vali.do("POST", fmt.Sprintf("/api/comments/%d/like", c.ID), nil, 200, &c)
	if c.Likes != 1 || !c.Liked {
		t.Errorf("comment like = %+v", c)
	}
	var cp struct {
		Items []struct {
			Likes int  `json:"likes"`
			Liked bool `json:"liked"`
		} `json:"items"`
	}
	vali.do("GET", fmt.Sprintf("/api/posts/%d/comments", p.ID), nil, 200, &cp)
	if len(cp.Items) != 1 || cp.Items[0].Likes != 1 || !cp.Items[0].Liked {
		t.Errorf("comments = %+v", cp)
	}
	var notes struct{ Items []struct{ Type string } }
	ali.do("GET", "/api/me/notifications", nil, 200, &notes)
	if len(notes.Items) == 0 || notes.Items[0].Type != "comment_like" {
		t.Errorf("notifications = %+v", notes)
	}
	vali.do("DELETE", fmt.Sprintf("/api/comments/%d/like", c.ID), nil, 200, &c)
	if c.Likes != 0 {
		t.Errorf("after unlike = %d", c.Likes)
	}
	vali.do("POST", "/api/comments/9999/like", nil, 404, nil)

	// Comment cursor paging.
	for i := range 4 {
		vali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": fmt.Sprint("c", i)}, 201, nil)
	}
	var walk struct {
		Items      []struct{ ID int } `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	ids := []int{}
	path := fmt.Sprintf("/api/posts/%d/comments?limit=2", p.ID)
	for {
		walk.NextCursor = "" // omitted on the last page
		anon.do("GET", path, nil, 200, &walk)
		for _, it := range walk.Items {
			ids = append(ids, it.ID)
		}
		if walk.NextCursor == "" {
			break
		}
		path = fmt.Sprintf("/api/posts/%d/comments?limit=2&cursor=%s", p.ID, walk.NextCursor)
	}
	if len(ids) != 5 || ids[0] != c.ID {
		t.Errorf("comment walk = %v", ids)
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d/comments?cursor=bad!", p.ID), nil, 400, nil)

	// Tag follows feed the feed.
	var feed page
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 0 {
		t.Fatalf("feed before follow = %d", feed.Total)
	}
	vali.do("POST", "/api/tags/%20KUBERNETES%20/follow", nil, 204, nil)
	vali.do("POST", "/api/tags/kubernetes/follow", nil, 204, nil)
	var tags []string
	vali.do("GET", "/api/me/tags", nil, 200, &tags)
	if fmt.Sprint(tags) != "[kubernetes]" {
		t.Errorf("tags = %v", tags)
	}
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 1 {
		t.Errorf("feed after tag follow = %d", feed.Total)
	}
	vali.do("DELETE", "/api/tags/kubernetes/follow", nil, 204, nil)
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 0 {
		t.Errorf("feed after unfollow = %d", feed.Total)
	}

	// Followers cursor paging.
	for _, n := range []string{"fan1", "fan2", "fan3"} {
		anon.register(n).do("POST", "/api/users/ali/follow", nil, 204, nil)
	}
	var fp struct {
		Items      []struct{ Username string } `json:"items"`
		NextCursor string                      `json:"next_cursor"`
	}
	anon.do("GET", "/api/users/ali/followers?limit=2", nil, 200, &fp)
	first, cursor := fp.Items, fp.NextCursor
	fp.Items, fp.NextCursor = nil, "" // fresh decode, no aliasing
	anon.do("GET", "/api/users/ali/followers?limit=2&cursor="+cursor, nil, 200, &fp)
	if len(first) != 2 || first[0].Username != "fan3" || len(fp.Items) != 1 || fp.Items[0].Username != "fan1" || fp.NextCursor != "" {
		t.Errorf("followers walk = %v then %v", first, fp.Items)
	}

	// Notifications cursor paging.
	var np struct {
		Items      []struct{ ID int64 } `json:"items"`
		NextCursor string               `json:"next_cursor"`
	}
	ali.do("GET", "/api/me/notifications?limit=2", nil, 200, &np)
	firstIDs := np.Items
	np.Items = nil
	ali.do("GET", "/api/me/notifications?limit=2&cursor="+np.NextCursor, nil, 200, &np)
	if len(firstIDs) != 2 || len(np.Items) == 0 || np.Items[0].ID >= firstIDs[1].ID {
		t.Errorf("notifications walk = %v then %v", firstIDs, np.Items)
	}

	// Per-author RSS.
	res, err := http.Get(e.srv.URL + "/api/users/ali/feed.xml")
	must(t, err)
	rss, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(rss), "<title>Kube</title>") {
		t.Errorf("user rss: %d %s", res.StatusCode, rss)
	}
	res, err = http.Get(e.srv.URL + "/api/users/nobody/feed.xml")
	must(t, err)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("unknown user rss = %d", res.StatusCode)
	}
}

func TestRequireVerifiedEmail(t *testing.T) {
	e := start(t, func(c *config.Config) { c.RequireVerifiedEmail = true })
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123") // seeded admins are verified
	ali := anon.register("ali")

	admin.do("POST", "/api/posts", map[string]any{"title": "Welcome"}, 201, nil)
	var pg page
	anon.do("GET", "/api/posts", nil, 200, &pg)
	postID := pg.Items[0].ID

	var errBody struct{ Error string }
	ali.do("POST", "/api/posts", map[string]any{"title": "x"}, 403, &errBody)
	if errBody.Error != "email not verified" {
		t.Errorf("error = %q", errBody.Error)
	}
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", postID), map[string]string{"text": "hi"}, 403, nil)
	ali.do("POST", "/api/publications", map[string]string{"slug": "ali-blog", "name": "Ali"}, 403, nil)
	ali.do("POST", fmt.Sprintf("/api/posts/%d/like", postID), nil, 200, nil) // reading and liking stay open

	var code string
	eventually(t, "verification email", func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if i := strings.Index(m.Text, "code="); i >= 0 && m.To == "ali@example.com" {
				code = strings.Fields(m.Text[i+len("code="):])[0]
				return true
			}
		}
		return false
	})
	anon.do("POST", "/api/auth/verify-email", map[string]string{"code": code}, 204, nil)
	ali.do("POST", "/api/posts", map[string]any{"title": "x"}, 201, nil)
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", postID), map[string]string{"text": "hi"}, 201, nil)
}

func TestDraftsAndScheduledPublishing(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	var d post
	ali.do("POST", "/api/posts", map[string]any{"title": "Secret draft", "status": "draft"}, 201, &d)
	anon.do("GET", fmt.Sprintf("/api/posts/%d", d.ID), nil, 404, nil)
	vali.do("GET", fmt.Sprintf("/api/posts/%d", d.ID), nil, 404, nil)
	ali.do("GET", fmt.Sprintf("/api/posts/%d", d.ID), nil, 200, nil)
	var drafts page
	ali.do("GET", "/api/me/posts?status=draft", nil, 200, &drafts)
	if drafts.Total != 1 {
		t.Errorf("drafts = %d", drafts.Total)
	}
	vali.do("GET", "/api/admin/posts?status=draft", nil, 403, nil)
	admin := anon.login("admin", "admin-pass-123")
	admin.do("GET", "/api/admin/posts?status=bogus", nil, 400, nil)
	admin.do("GET", "/api/admin/posts?status=draft", nil, 200, &drafts)
	if drafts.Total != 1 {
		t.Errorf("admin drafts = %d", drafts.Total)
	}

	ali.do("POST", "/api/posts", map[string]any{"title": "x", "status": "scheduled",
		"publish_at": time.Now().Add(-time.Minute)}, 400, nil)
	var s post
	ali.do("POST", "/api/posts", map[string]any{"title": "Coming soon", "status": "scheduled",
		"publish_at": time.Now().Add(2 * time.Second)}, 201, &s)
	if s.Status != "scheduled" {
		t.Fatalf("status = %s", s.Status)
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d", s.ID), nil, 404, nil)
	eventually(t, "scheduled post published by the queue", func() bool {
		return anon.status("GET", fmt.Sprintf("/api/posts/%d", s.ID), nil) == 200
	})
}

func TestAccountLifecycle(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")

	// Bad registrations.
	anon.do("POST", "/api/auth/register", map[string]string{"username": "ali", "email": "other@example.com", "password": "secret123"}, 409, nil)
	anon.do("POST", "/api/auth/register", map[string]string{"username": "x1y", "email": "x@mailinator.com", "password": "secret123"}, 400, nil)

	// Refresh rotates the token.
	var s session
	ali.do("POST", "/api/auth/refresh", nil, 200, &s)
	ali.do("GET", "/api/me", nil, 401, nil) // old token revoked
	ali = ali.as(s)
	ali.do("GET", "/api/me", nil, 200, nil)

	// Password change ends other sessions.
	other := anon.login("ali@example.com", "secret123")
	ali.do("PUT", "/api/me/password", map[string]string{"old_password": "secret123", "new_password": "newsecret1"}, 200, &s)
	other.do("GET", "/api/me", nil, 401, nil)
	ali = ali.as(s)

	// Forgot / reset password through email (Outbox in tests).
	anon.do("POST", "/api/auth/forgot-password", map[string]string{"email": "nobody@example.com"}, 202, nil)
	anon.do("POST", "/api/auth/forgot-password", map[string]string{"email": "ali@example.com"}, 202, nil)
	var token string
	eventually(t, "reset email", func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if i := strings.Index(m.Text, "token="); i >= 0 && m.To == "ali@example.com" {
				token = strings.Fields(m.Text[i+len("token="):])[0]
				return true
			}
		}
		return false
	})
	anon.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "password": "reset-pass1"}, 204, nil)
	anon.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "password": "reset-pass2"}, 400, nil)
	ali.do("GET", "/api/me", nil, 401, nil)
	ali = anon.login("ali", "reset-pass1")

	// Delete account: grace period, then login restores it.
	ali.do("POST", "/api/posts", map[string]any{"title": "mine"}, 201, nil)
	ali.do("DELETE", "/api/me", map[string]string{"password": "wrong-pass"}, 401, nil)
	var del struct {
		PurgeAt time.Time `json:"purge_at"`
	}
	ali.do("DELETE", "/api/me", map[string]string{"password": "reset-pass1"}, 202, &del)
	if d := time.Until(del.PurgeAt); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("purge_at in %v", d)
	}
	ali.do("GET", "/api/me", nil, 401, nil)
	anon.do("GET", "/api/users/ali", nil, 404, nil)
	var posts page
	anon.do("GET", "/api/posts", nil, 200, &posts)
	if posts.Total != 0 {
		t.Errorf("deleted user's posts still listed: %d", posts.Total)
	}
	ali = anon.login("ali", "reset-pass1") // restores
	anon.do("GET", "/api/users/ali", nil, 200, nil)
}

func TestUploadsToMinIO(t *testing.T) {
	e := start(t)
	ali := e.anon(t).register("ali")

	img := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	for x := range 1200 {
		img.Set(x, 300, color.RGBA{R: 255, A: 255})
	}
	var pngBuf bytes.Buffer
	must(t, png.Encode(&pngBuf, img))

	type uploadOut struct {
		URL, ThumbnailURL, SrcSet string
		Variants                  map[string]string
	}
	upload := func(name string, data []byte, want int) (out uploadOut) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(data)
		mw.Close()
		req, _ := http.NewRequest("POST", ali.base+"/api/uploads", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		var raw struct {
			URL          string            `json:"url"`
			ThumbnailURL string            `json:"thumbnail_url"`
			Variants     map[string]string `json:"variants"`
			SrcSet       string            `json:"srcset"`
		}
		ali.send(req, want, &raw)
		return uploadOut{raw.URL, raw.ThumbnailURL, raw.SrcSet, raw.Variants}
	}
	upload("x.html", []byte("<script>alert(1)</script>"), 400)
	u := upload("x.png", pngBuf.Bytes(), 201)

	res, err := http.Get(e.srv.URL + u.URL)
	must(t, err)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("GET %s: %d %s", u.URL, res.StatusCode, res.Header.Get("Content-Type"))
	}
	eventually(t, "thumbnail", func() bool {
		res, err := http.Get(e.srv.URL + u.ThumbnailURL)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return false
		}
		thumb, _, err := image.Decode(res.Body)
		return err == nil && thumb.Bounds().Dx() == 480 && thumb.Bounds().Dy() == 240
	})

	// WebP variants are written before the thumbnail, so they exist now.
	if len(u.Variants) != 3 || !strings.Contains(u.SrcSet, " 640w") {
		t.Fatalf("variants = %v srcset = %q", u.Variants, u.SrcSet)
	}
	for w, url := range map[string]string{"640": u.Variants["640"], "1280": u.Variants["1280"]} {
		res, err := http.Get(e.srv.URL + url)
		must(t, err)
		img, err := webp.Decode(res.Body)
		res.Body.Close()
		if err != nil || res.Header.Get("Content-Type") != "image/webp" {
			t.Fatalf("variant %s: %v %s", w, err, res.Header.Get("Content-Type"))
		}
		want := map[string]int{"640": 640, "1280": 1200}[w] // 1200 wide original: not upscaled
		if img.Bounds().Dx() != want {
			t.Errorf("variant %s width = %d, want %d", w, img.Bounds().Dx(), want)
		}
	}
}

func TestRateLimitsAndHealth(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	anon.do("GET", "/api/healthz", nil, 200, nil)
	if code := anon.status("GET", "/api/docs/openapi.json", nil); code != 200 {
		t.Errorf("docs = %d", code)
	}

	ali := anon.register("ali")
	ali.do("POST", "/api/posts", map[string]any{"title": "x"}, 201, &post{})
	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "y"}, 201, &p)
	for i := range 9 {
		ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": fmt.Sprint(i)}, 201, nil)
	}
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "10th"}, 201, nil)
	ali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "11th"}, 429, nil)

	codes := map[int]int{}
	for range 12 {
		codes[anon.status("POST", "/api/auth/login", map[string]string{"login": "nobody", "password": "wrongpass"})]++
	}
	if codes[429] == 0 {
		t.Errorf("login never rate limited: %v", codes)
	}
}
