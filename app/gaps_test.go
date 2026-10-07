package app_test

// Endpoints the scenario tests above do not reach: profile edits, unfollow,
// taxonomy cleanup, series edits, mutes, backup code rotation, logout and
// the Server-Sent Event streams.

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// sse opens path as an event stream and returns a channel of its data lines.
func (c *client) sse(path string) <-chan string {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	must(c.t, err)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		res.Body.Close()
		c.t.Fatalf("GET %s = %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan string, 16)
	connected := make(chan struct{})
	go func() {
		defer res.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			line := sc.Text()
			if line == ": connected" {
				close(connected)
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				out <- data
			}
		}
	}()
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		c.t.Fatalf("%s: no connected comment", path)
	}
	return out
}

func waitEvent(t *testing.T, ch <-chan string, contains string) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed before %q", contains)
			}
			if strings.Contains(m, contains) {
				return
			}
		case <-timeout:
			t.Fatalf("no event containing %q", contains)
		}
	}
}

func TestProfilesFollowsAndAdminUsers(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")
	vali := anon.register("vali")

	// Profile edit: trimmed, validated.
	var me struct {
		DisplayName string   `json:"display_name"`
		Bio         string   `json:"bio"`
		AvatarURL   string   `json:"avatar_url"`
		Roles       []string `json:"roles"`
	}
	ali.do("PATCH", "/api/me", map[string]string{"display_name": "  Ali V  ", "bio": "gopher", "avatar_url": "https://x.test/a.png"}, 200, &me)
	if me.DisplayName != "Ali V" || me.Bio != "gopher" || len(me.Roles) == 0 {
		t.Errorf("me = %+v", me)
	}
	ali.do("PATCH", "/api/me", map[string]string{"bio": strings.Repeat("b", 301)}, 400, nil)
	ali.do("PATCH", "/api/me", map[string]string{"avatar_url": "javascript:alert(1)"}, 400, nil)
	anon.do("PATCH", "/api/me", map[string]string{"bio": "x"}, 401, nil)
	var prof struct {
		DisplayName string `json:"display_name"`
		Followers   int    `json:"followers"`
	}
	anon.do("GET", "/api/users/ali", nil, 200, &prof)
	if prof.DisplayName != "Ali V" {
		t.Errorf("profile = %+v", prof)
	}

	// Follow, list following, unfollow (idempotent).
	vali.do("POST", "/api/users/ali/follow", nil, 204, nil)
	var following struct {
		Items []struct{ Username string } `json:"items"`
		Total int                         `json:"total"`
	}
	anon.do("GET", "/api/users/vali/following", nil, 200, &following)
	if following.Total != 1 || following.Items[0].Username != "ali" {
		t.Errorf("following = %+v", following)
	}
	anon.do("GET", "/api/users/nobody/following", nil, 404, nil)
	vali.do("DELETE", "/api/users/ali/follow", nil, 204, nil)
	vali.do("DELETE", "/api/users/ali/follow", nil, 204, nil)
	vali.do("DELETE", "/api/users/nobody/follow", nil, 404, nil)
	anon.do("GET", "/api/users/vali/following", nil, 200, &following)
	anon.do("GET", "/api/users/ali", nil, 200, &prof)
	if following.Total != 0 || prof.Followers != 0 {
		t.Errorf("after unfollow: following %d, followers %d", following.Total, prof.Followers)
	}

	// Admin user list (user.read) with search.
	ali.do("GET", "/api/admin/users", nil, 403, nil)
	var users struct {
		Items []struct {
			Username string
			Roles    []string
		} `json:"items"`
		Total int `json:"total"`
	}
	admin.do("GET", "/api/admin/users?limit=50", nil, 200, &users)
	if users.Total != 3 {
		t.Errorf("users = %+v", users)
	}
	for _, u := range users.Items {
		if u.Username == "admin" && !slices.Contains(u.Roles, "super_admin") {
			t.Errorf("admin roles = %v", u.Roles)
		}
	}
	admin.do("GET", "/api/admin/users?q=val", nil, 200, &users)
	if users.Total != 1 || users.Items[0].Username != "vali" {
		t.Errorf("search = %+v", users)
	}
}

func TestTaxonomyBookmarksAndPostDelete(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	ali := anon.register("ali")
	vali := anon.register("vali")

	var cat, empty, label struct{ ID int }
	admin.do("POST", "/api/categories", map[string]string{"name": "Go"}, 201, &cat)
	admin.do("POST", "/api/categories", map[string]string{"name": "Empty"}, 201, &empty)
	admin.do("POST", "/api/labels", map[string]string{"name": "Guide", "color": "#22aa66"}, 201, &label)

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Go tips", "body": "x", "category_id": cat.ID,
		"label_ids": []int{label.ID}, "tags": []string{"go", "tips"}}, 201, &p)
	var draft post
	ali.do("POST", "/api/posts", map[string]any{"title": "Draft", "status": "draft", "category_id": cat.ID}, 201, &draft)

	// Category posts: published only; unknown category is 404.
	var inCat page
	anon.do("GET", fmt.Sprintf("/api/categories/%d/posts", cat.ID), nil, 200, &inCat)
	if inCat.Total != 1 || inCat.Items[0].ID != p.ID {
		t.Errorf("category posts = %+v", inCat)
	}
	anon.do("GET", "/api/categories/999999/posts", nil, 404, nil)
	anon.do("GET", "/api/categories/abc/posts", nil, 400, nil)

	// Tags and labels are public lists.
	var tags []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	anon.do("GET", "/api/tags", nil, 200, &tags)
	got := map[string]int{}
	for _, tg := range tags {
		got[tg.Name] = tg.Count
	}
	if got["go"] != 1 || got["tips"] != 1 {
		t.Errorf("tags = %+v", tags)
	}
	var labels []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	anon.do("GET", "/api/labels", nil, 200, &labels)
	if len(labels) != 1 || labels[0].Name != "Guide" {
		t.Errorf("labels = %+v", labels)
	}

	// Deleting taxonomy needs category.write / label.write.
	ali.do("DELETE", fmt.Sprintf("/api/categories/%d", empty.ID), nil, 403, nil)
	admin.do("DELETE", fmt.Sprintf("/api/categories/%d", empty.ID), nil, 204, nil)
	admin.do("DELETE", fmt.Sprintf("/api/categories/%d", empty.ID), nil, 404, nil)
	anon.do("GET", fmt.Sprintf("/api/categories/%d/posts", empty.ID), nil, 404, nil)
	ali.do("DELETE", fmt.Sprintf("/api/labels/%d", label.ID), nil, 403, nil)
	admin.do("DELETE", fmt.Sprintf("/api/labels/%d", label.ID), nil, 204, nil)
	admin.do("DELETE", fmt.Sprintf("/api/labels/%d", label.ID), nil, 404, nil)
	anon.do("GET", "/api/labels", nil, 200, &labels)
	ali.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 200, &p)
	if len(labels) != 0 || len(p.Labels) != 0 {
		t.Errorf("label left: %+v / %+v", labels, p.Labels)
	}

	// Bookmark then unbookmark (idempotent).
	vali.do("POST", fmt.Sprintf("/api/posts/%d/bookmark", p.ID), nil, 204, nil)
	vali.do("DELETE", fmt.Sprintf("/api/posts/%d/bookmark", p.ID), nil, 204, nil)
	vali.do("DELETE", fmt.Sprintf("/api/posts/%d/bookmark", p.ID), nil, 204, nil)
	var bm page
	vali.do("GET", "/api/me/bookmarks", nil, 200, &bm)
	if bm.Total != 0 {
		t.Errorf("bookmarks = %d", bm.Total)
	}

	// Post delete: owner only (ABAC); gone afterwards.
	anon.do("DELETE", fmt.Sprintf("/api/posts/%d", p.ID), nil, 401, nil)
	vali.do("DELETE", fmt.Sprintf("/api/posts/%d", p.ID), nil, 403, nil)
	ali.do("DELETE", fmt.Sprintf("/api/posts/%d", p.ID), nil, 204, nil)
	ali.do("DELETE", fmt.Sprintf("/api/posts/%d", p.ID), nil, 404, nil)
	anon.do("GET", fmt.Sprintf("/api/posts/%d", p.ID), nil, 404, nil)
	anon.do("GET", fmt.Sprintf("/api/categories/%d/posts", cat.ID), nil, 200, &inCat)
	if inCat.Total != 0 {
		t.Errorf("deleted post still in category: %d", inCat.Total)
	}
}

func TestSeriesEditMutesAndUnsubscribe(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	var s struct {
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	ali.do("POST", "/api/series", map[string]string{"title": "Learn Go"}, 201, &s)
	vali.do("PATCH", "/api/series/"+s.Slug, map[string]string{"title": "Mine now"}, 403, nil)
	anon.do("PATCH", "/api/series/"+s.Slug, map[string]string{"title": "x"}, 401, nil)
	ali.do("PATCH", "/api/series/"+s.Slug, map[string]string{"title": ""}, 400, nil)
	ali.do("PATCH", "/api/series/nope", map[string]string{"title": "x"}, 404, nil)
	ali.do("PATCH", "/api/series/"+s.Slug, map[string]string{"title": "Learn Go, part 2", "description": "basics"}, 200, &s)
	if s.Title != "Learn Go, part 2" || s.Description != "basics" {
		t.Errorf("series = %+v", s)
	}

	// Mute: listed under /me/mutes, unmute removes it.
	var mutes struct {
		Items []struct{ Username string } `json:"items"`
		Total int                         `json:"total"`
	}
	vali.do("POST", "/api/users/ali/mute", nil, 204, nil)
	vali.do("GET", "/api/me/mutes", nil, 200, &mutes)
	if mutes.Total != 1 || mutes.Items[0].Username != "ali" {
		t.Errorf("mutes = %+v", mutes)
	}
	vali.do("DELETE", "/api/users/ali/mute", nil, 204, nil)
	vali.do("DELETE", "/api/users/nobody/mute", nil, 404, nil)
	vali.do("GET", "/api/me/mutes", nil, 200, &mutes)
	if mutes.Total != 0 {
		t.Errorf("mutes after unmute = %+v", mutes)
	}
	anon.do("GET", "/api/me/mutes", nil, 401, nil)

	// Unsubscribe from an author's emails.
	e.verify(t, "vali")
	var prof struct {
		IsSubscribed bool `json:"is_subscribed"`
	}
	vali.do("POST", "/api/users/ali/subscribe", nil, 204, nil)
	vali.do("DELETE", "/api/users/ali/subscribe", nil, 204, nil)
	vali.do("GET", "/api/users/ali", nil, 200, &prof)
	if prof.IsSubscribed {
		t.Error("still subscribed")
	}
	vali.do("DELETE", "/api/users/nobody/subscribe", nil, 404, nil)
}

func TestBackupCodesAndLogout(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")

	// Backup code rotation needs 2FA on and a current code.
	ali.do("POST", "/api/me/2fa/backup-codes", map[string]string{"code": "123456"}, 400, nil)
	var setup struct {
		Secret string `json:"secret"`
	}
	ali.do("POST", "/api/me/2fa/setup", nil, 200, &setup)
	var first, second struct {
		BackupCodes []string `json:"backup_codes"`
	}
	ali.do("POST", "/api/me/2fa/enable", map[string]string{"code": totp(t, setup.Secret, 0)}, 200, &first)
	ali.do("POST", "/api/me/2fa/backup-codes", map[string]string{"code": "000000"}, 400, nil)
	ali.do("POST", "/api/me/2fa/backup-codes", map[string]string{"code": first.BackupCodes[0]}, 200, &second)
	if len(second.BackupCodes) != 10 || second.BackupCodes[0] == first.BackupCodes[0] {
		t.Fatalf("new codes = %v", second.BackupCodes)
	}
	var status struct {
		Left int `json:"backup_codes_left"`
	}
	ali.do("GET", "/api/me/2fa", nil, 200, &status)
	if status.Left != 10 {
		t.Errorf("backup codes left = %d", status.Left)
	}
	// Old codes are void.
	ali.do("POST", "/api/me/2fa/disable", map[string]string{"password": "secret123", "code": first.BackupCodes[1]}, 400, nil)
	ali.do("POST", "/api/me/2fa/disable", map[string]string{"password": "secret123", "code": second.BackupCodes[0]}, 204, nil)

	// Logout ends this session only; logout-all ends the rest.
	other := anon.login("ali", "secret123")
	third := anon.login("ali", "secret123")
	ali.do("POST", "/api/auth/logout", nil, 204, nil)
	ali.do("GET", "/api/me", nil, 401, nil)
	other.do("GET", "/api/me", nil, 200, nil)
	anon.do("POST", "/api/auth/logout-all", nil, 401, nil)
	other.do("POST", "/api/auth/logout-all", nil, 204, nil)
	other.do("GET", "/api/me", nil, 401, nil)
	third.do("GET", "/api/me", nil, 401, nil)
}

func TestEventStreams(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")

	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Live", "body": "x"}, 201, &p)

	// Comment stream: public, 404 for an unknown post.
	anon.do("GET", "/api/posts/999999/comments/stream", nil, 404, nil)
	comments := anon.sse(fmt.Sprintf("/api/posts/%d/comments/stream", p.ID))

	// Notification stream: EventSource cannot send headers, so it takes a
	// one-time ticket.
	anon.do("POST", "/api/me/stream-ticket", nil, 401, nil)
	anon.do("GET", "/api/me/notifications/stream", nil, 401, nil)
	var tk struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expires_in"`
	}
	ali.do("POST", "/api/me/stream-ticket", nil, 201, &tk)
	if tk.Ticket == "" || tk.ExpiresIn <= 0 {
		t.Fatalf("ticket = %+v", tk)
	}
	notes := anon.sse("/api/me/notifications/stream?ticket=" + tk.Ticket)
	// Single use.
	anon.do("GET", "/api/me/notifications/stream?ticket="+tk.Ticket, nil, 401, nil)

	vali.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "hello live"}, 201, nil)
	waitEvent(t, comments, "hello live")
	waitEvent(t, notes, "comment")

	// Mark read: no ids marks nothing; listed ids are marked.
	var n struct {
		Unread int `json:"unread"`
		Items  []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	ali.do("POST", "/api/me/notifications/read", map[string]any{}, 204, nil)
	ali.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.Unread != 1 || len(n.Items) != 1 {
		t.Fatalf("notifications = %+v", n)
	}
	ali.do("POST", "/api/me/notifications/read", map[string]any{"ids": []int64{n.Items[0].ID}}, 204, nil)
	ali.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.Unread != 0 {
		t.Errorf("unread = %d", n.Unread)
	}
}
