package app_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/config"
)

// verify confirms name's email with the code from the outbox.
func (e *env) verify(t *testing.T, name string) {
	t.Helper()
	var code string
	eventually(t, "verification email to "+name, func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if i := strings.Index(m.Text, "code="); i >= 0 && m.To == name+"@example.com" {
				code = strings.Fields(m.Text[i+len("code="):])[0]
				return true
			}
		}
		return false
	})
	e.anon(t).do("POST", "/api/auth/verify-email", map[string]string{"code": code}, 204, nil)
}

type list struct {
	Slug     string `json:"slug"`
	Private  bool   `json:"private"`
	Posts    int    `json:"posts"`
	Contains *bool  `json:"contains"`
}

func TestListsPinAndUnlisted(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	var a, b, u, draft post
	ali.do("POST", "/api/posts", map[string]any{"title": "A", "body": "a"}, 201, &a)
	ali.do("POST", "/api/posts", map[string]any{"title": "B", "body": "b"}, 201, &b)
	ali.do("POST", "/api/posts", map[string]any{"title": "Secret link", "body": "u", "status": "unlisted"}, 201, &u)
	ali.do("POST", "/api/posts", map[string]any{"title": "Draft", "status": "draft"}, 201, &draft)

	// Unlisted: readable by link, missing from listings.
	anon.do("GET", fmt.Sprintf("/api/posts/%d", u.ID), nil, 200, nil)
	anon.do("GET", "/api/slug/"+u.Slug, nil, 200, nil)
	var pg page
	anon.do("GET", "/api/posts", nil, 200, &pg)
	anon.do("GET", "/api/posts?q=Secret", nil, 200, &pg)
	if pg.Total != 0 {
		t.Errorf("unlisted in search: %+v", pg.Items)
	}
	anon.do("GET", "/api/users/ali/posts", nil, 200, &pg)
	if pg.Total != 2 {
		t.Errorf("profile posts = %d", pg.Total)
	}
	ali.do("GET", "/api/me/posts?status=unlisted", nil, 200, &pg)
	if pg.Total != 1 {
		t.Errorf("my unlisted = %d", pg.Total)
	}

	// Pin: own published posts only.
	ali.do("PUT", "/api/me/pin", map[string]int{"post_id": draft.ID}, 400, nil)
	vali.do("PUT", "/api/me/pin", map[string]int{"post_id": a.ID}, 400, nil)
	ali.do("PUT", "/api/me/pin", map[string]int{"post_id": b.ID}, 204, nil)
	var prof struct {
		PinnedPostID int `json:"pinned_post_id"`
	}
	anon.do("GET", "/api/users/ali", nil, 200, &prof)
	if prof.PinnedPostID != b.ID {
		t.Errorf("pinned = %d", prof.PinnedPostID)
	}
	ali.do("DELETE", "/api/me/pin", nil, 204, nil)
	anon.do("GET", "/api/users/ali", nil, 200, &prof)
	if prof.PinnedPostID != 0 {
		t.Error("still pinned")
	}

	// Lists: public and private, owner-only writes.
	var pub, priv list
	vali.do("POST", "/api/lists", map[string]any{"name": "Go reads"}, 201, &pub)
	vali.do("POST", "/api/lists", map[string]any{"name": "Later", "private": true}, 201, &priv)
	vali.do("POST", "/api/lists", map[string]any{"name": " "}, 400, nil)
	vali.do("PUT", "/api/lists/"+pub.Slug+fmt.Sprintf("/posts/%d", a.ID), nil, 204, nil)
	vali.do("PUT", "/api/lists/"+pub.Slug+fmt.Sprintf("/posts/%d", a.ID), nil, 204, nil) // idempotent
	vali.do("PUT", "/api/lists/"+pub.Slug+fmt.Sprintf("/posts/%d", draft.ID), nil, 404, nil)
	vali.do("PUT", "/api/lists/"+priv.Slug+fmt.Sprintf("/posts/%d", b.ID), nil, 204, nil)
	ali.do("PUT", "/api/lists/"+pub.Slug+fmt.Sprintf("/posts/%d", b.ID), nil, 403, nil)
	ali.do("PUT", "/api/lists/"+priv.Slug+fmt.Sprintf("/posts/%d", b.ID), nil, 404, nil)

	anon.do("GET", "/api/lists/"+pub.Slug+"/posts", nil, 200, &pg)
	if pg.Total != 1 || pg.Items[0].ID != a.ID {
		t.Errorf("list posts = %+v", pg)
	}
	anon.do("GET", "/api/lists/"+priv.Slug, nil, 404, nil)
	vali.do("GET", "/api/lists/"+priv.Slug+"/posts", nil, 200, &pg)
	var lists struct {
		Items []list
		Total int
	}
	anon.do("GET", "/api/users/vali/lists", nil, 200, &lists)
	if lists.Total != 1 || lists.Items[0].Slug != pub.Slug || lists.Items[0].Posts != 1 {
		t.Errorf("public lists = %+v", lists)
	}
	vali.do("GET", fmt.Sprintf("/api/me/lists?post_id=%d", b.ID), nil, 200, &lists)
	l := lists.Items
	if len(l) != 2 || l[0].Contains == nil || !*l[0].Contains || *l[1].Contains {
		t.Errorf("my lists = %+v", lists)
	}
	vali.do("GET", "/api/me/lists?limit=1&page=2", nil, 200, &lists)
	if lists.Total != 2 || len(lists.Items) != 1 || lists.Items[0].Slug != pub.Slug {
		t.Errorf("lists page 2 = %+v", lists)
	}
	vali.do("DELETE", "/api/lists/"+pub.Slug+fmt.Sprintf("/posts/%d", a.ID), nil, 204, nil)
	vali.do("PATCH", "/api/lists/"+pub.Slug, map[string]any{"name": "Go", "private": true}, 200, nil)
	anon.do("GET", "/api/lists/"+pub.Slug, nil, 404, nil)
	vali.do("DELETE", "/api/lists/"+pub.Slug, nil, 204, nil)
	vali.do("GET", "/api/lists/"+pub.Slug, nil, 404, nil)
}

func TestReadsStatsHidesAndForYou(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	gani := anon.register("gani")

	var k8s, cook, other post
	ali.do("POST", "/api/posts", map[string]any{"title": "K8s", "body": "pods", "tags": []string{"k8s"}}, 201, &k8s)
	gani.do("POST", "/api/posts", map[string]any{"title": "Cooking", "body": "rice", "tags": []string{"food"}}, 201, &cook)
	gani.do("POST", "/api/posts", map[string]any{"title": "Helm", "body": "charts", "tags": []string{"k8s"}}, 201, &other)

	// Reads: once per reader, not short visits, not the author.
	var res struct{ Counted bool }
	path := fmt.Sprintf("/api/posts/%d/read", k8s.ID)
	vali.do("POST", path, map[string]any{"progress": 0.2, "seconds": 5}, 200, &res)
	if res.Counted {
		t.Error("short visit counted")
	}
	vali.do("POST", path, map[string]any{"progress": 0.9}, 200, &res)
	if !res.Counted {
		t.Error("read not counted")
	}
	vali.do("POST", path, map[string]any{"progress": 1}, 200, &res)
	ali.do("POST", path, map[string]any{"progress": 1}, 200, &res)
	if res.Counted {
		t.Error("author read counted")
	}
	vali.do("POST", path, map[string]any{"progress": 2}, 400, nil)
	vali.do("POST", fmt.Sprintf("/api/posts/%d/view", k8s.ID), nil, 204, nil)
	gani.do("POST", fmt.Sprintf("/api/posts/%d/view", k8s.ID), nil, 204, nil)

	var stats struct {
		Items []struct {
			PostID    int     `json:"post_id"`
			Reads     int     `json:"reads"`
			ReadRatio float64 `json:"read_ratio"`
		}
		Totals struct{ Reads int }
	}
	ali.do("GET", "/api/me/stats", nil, 200, &stats)
	if stats.Totals.Reads != 1 || stats.Items[0].Reads != 1 || stats.Items[0].ReadRatio != 0.5 {
		t.Errorf("stats = %+v", stats)
	}
	var daily struct {
		Days []struct {
			Date  string
			Views int64
			Reads int
		}
	}
	ali.do("GET", fmt.Sprintf("/api/me/stats/%d?days=7", k8s.ID), nil, 200, &daily)
	today := daily.Days[len(daily.Days)-1]
	if len(daily.Days) != 7 || today.Date != time.Now().UTC().Format("2006-01-02") || today.Views != 2 || today.Reads != 1 {
		t.Errorf("daily = %+v", daily.Days)
	}
	vali.do("GET", fmt.Sprintf("/api/me/stats/%d", k8s.ID), nil, 403, nil)

	// For You: followed tags first; already read posts left out.
	vali.do("POST", "/api/tags/k8s/follow", nil, 204, nil)
	var fyPage struct {
		Items []post
		Total int
	}
	vali.do("GET", "/api/me/for-you", nil, 200, &fyPage)
	fy := fyPage.Items
	if len(fy) != 2 || fy[0].ID != other.ID || fy[1].ID != cook.ID {
		t.Errorf("for you = %+v", fy)
	}

	// Show less: hide a tag, an author, a post.
	vali.do("POST", "/api/me/hidden", map[string]string{"kind": "tag", "target": "K8s"}, 204, nil)
	vali.do("GET", "/api/me/for-you", nil, 200, &fyPage)
	fy = fyPage.Items
	if len(fy) != 1 || fy[0].ID != cook.ID {
		t.Errorf("for you after hiding k8s = %+v", fy)
	}
	vali.do("POST", "/api/me/hidden", map[string]string{"kind": "author", "target": "gani"}, 204, nil)
	vali.do("POST", "/api/me/hidden", map[string]string{"kind": "post", "target": fmt.Sprint(k8s.ID)}, 204, nil)
	vali.do("POST", "/api/me/hidden", map[string]string{"kind": "mood", "target": "x"}, 400, nil)
	vali.do("POST", "/api/me/hidden", map[string]string{"kind": "author", "target": "nobody"}, 404, nil)
	vali.do("GET", "/api/me/for-you", nil, 200, &fyPage)
	fy = fyPage.Items
	if len(fy) != 0 {
		t.Errorf("for you after hiding everything = %+v", fy)
	}
	var hidden struct {
		Items []struct{ Kind, Target string }
		Total int
	}
	vali.do("GET", "/api/me/hidden?limit=2&page=1", nil, 200, &hidden)
	if hidden.Total != 3 || len(hidden.Items) != 2 || hidden.Items[1].Target != "gani" {
		t.Errorf("hidden = %+v", hidden)
	}
	vali.do("POST", "/api/users/gani/follow", nil, 204, nil)
	var feed page
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 0 {
		t.Errorf("feed shows hidden posts: %+v", feed.Items)
	}
	vali.do("DELETE", "/api/me/hidden/author/gani", nil, 204, nil)
	vali.do("DELETE", "/api/me/hidden/tag/k8s", nil, 204, nil)
	vali.do("GET", "/api/me/feed", nil, 200, &feed)
	if feed.Total != 2 {
		t.Errorf("feed after unhide = %d", feed.Total)
	}
}

func TestEmailSubscriptions(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	// Subscribing needs a verified email.
	vali.do("POST", "/api/users/ali/subscribe", nil, 403, nil)
	e.verify(t, "vali")
	vali.do("POST", "/api/users/ali/subscribe", nil, 204, nil)
	vali.do("POST", "/api/users/vali/subscribe", nil, 400, nil)
	var prof struct {
		IsSubscribed bool `json:"is_subscribed"`
	}
	vali.do("GET", "/api/users/ali", nil, 200, &prof)
	if !prof.IsSubscribed {
		t.Error("not subscribed")
	}

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "News", "body": "hello"}, 201, &p)
	eventually(t, "new post email", func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if m.To == "vali@example.com" && strings.Contains(m.Text, "http://site.test/p/"+p.Slug) {
				return true
			}
		}
		return false
	})

	// One email per post, with a working one-click unsubscribe link.
	var unsub string
	n := 0
	for _, m := range e.app.Outbox.Sent() {
		if m.To == "vali@example.com" && strings.Contains(m.Text, "/p/"+p.Slug) {
			n++
			unsub = m.Unsubscribe
		}
	}
	if n != 1 || !strings.HasPrefix(unsub, "http://localhost/api/unsubscribe?") {
		t.Fatalf("emails = %d, unsubscribe = %q", n, unsub)
	}
	path := strings.TrimPrefix(unsub, "http://localhost")
	anon.do("POST", path+"x", nil, 400, nil)
	anon.do("POST", path, nil, 204, nil)
	vali.do("GET", "/api/users/ali", nil, 200, &prof)
	if prof.IsSubscribed {
		t.Error("still subscribed")
	}
}

func TestImportStory(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>My old post</title><meta name="description" content="from my blog"></head>
<body><nav>menu</nav><article><h1>My old post</h1><p>Hello <em>there</em>.</p></article></body></html>`)
	}))
	defer site.Close()

	e := start(t, func(c *config.Config) { c.ImportAllowPrivate = true })
	ali := e.anon(t).register("ali")
	var p struct {
		Title        string `json:"title"`
		Subtitle     string `json:"subtitle"`
		Body         string `json:"body"`
		Status       string `json:"status"`
		CanonicalURL string `json:"canonical_url"`
	}
	ali.do("POST", "/api/posts/import", map[string]string{"url": site.URL + "/old"}, 201, &p)
	if p.Title != "My old post" || p.Subtitle != "from my blog" || p.Body != "Hello *there*." ||
		p.Status != "draft" || p.CanonicalURL != site.URL+"/old" {
		t.Errorf("imported = %+v", p)
	}
	ali.do("POST", "/api/posts/import", map[string]string{"url": "ftp://x"}, 400, nil)
}

func TestImportRefusesInternalAddresses(t *testing.T) {
	e := start(t)
	ali := e.anon(t).register("ali")
	var res struct{ Error string }
	ali.do("POST", "/api/posts/import", map[string]string{"url": e.srv.URL + "/api/healthz"}, 400, &res)
	if !strings.Contains(res.Error, "private address") {
		t.Errorf("error = %q", res.Error)
	}
}
