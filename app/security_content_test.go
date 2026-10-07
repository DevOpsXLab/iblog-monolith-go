package app_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/mfa"
)

func totp(t *testing.T, secret string, offset int64) string {
	t.Helper()
	c, err := mfa.Code(secret, mfa.Step(time.Now())+offset)
	must(t, err)
	return c
}

type loginReply struct {
	Token       string `json:"token"`
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
}

func TestTwoFactorAndSessions(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	creds := map[string]string{"login": "ali", "password": "secret123"}

	var setup struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_url"`
	}
	ali.do("POST", "/api/me/2fa/setup", nil, 200, &setup)
	ali.do("POST", "/api/me/2fa/enable", map[string]string{"code": "000000"}, 400, nil)
	var codes struct {
		BackupCodes []string `json:"backup_codes"`
	}
	ali.do("POST", "/api/me/2fa/enable", map[string]string{"code": totp(t, setup.Secret, 0)}, 200, &codes)
	if len(codes.BackupCodes) != 10 {
		t.Fatalf("backup codes = %v", codes.BackupCodes)
	}
	ali.do("POST", "/api/me/2fa/setup", nil, 400, nil) // already on
	var status struct {
		Enabled bool `json:"enabled"`
		Left    int  `json:"backup_codes_left"`
	}
	ali.do("GET", "/api/me/2fa", nil, 200, &status)
	if !status.Enabled || status.Left != 10 {
		t.Fatalf("status = %+v", status)
	}

	// Password alone no longer logs in; Guard's own login is closed.
	var ch loginReply
	anon.do("POST", "/api/auth/login", creds, 200, &ch)
	if !ch.MFARequired || ch.MFAToken == "" || ch.Token != "" {
		t.Fatalf("login = %+v", ch)
	}
	anon.do("POST", "/api/auth/login", map[string]string{"login": "ali", "password": "wrong-pass"}, 401, nil)
	anon.do("POST", "/api/guard/auth/login", map[string]string{"email": "ali@example.com", "password": "secret123"}, 404, nil)

	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": "000000"}, 400, nil)
	var s session
	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": codes.BackupCodes[0]}, 200, &s)
	if s.Token == "" || s.User.ID != ali.user.ID {
		t.Fatalf("session = %+v", s)
	}
	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": codes.BackupCodes[1]}, 400, nil) // token used

	// A backup code works once; the TOTP code used at enable cannot be replayed.
	anon.do("POST", "/api/auth/login", creds, 200, &ch)
	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": codes.BackupCodes[0]}, 400, nil)
	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": totp(t, setup.Secret, -1)}, 400, nil)
	var s2 session
	anon.do("POST", "/api/auth/login/2fa", map[string]string{"mfa_token": ch.MFAToken, "code": totp(t, setup.Secret, 1)}, 200, &s2)
	phone := anon.as(s2)

	// Sessions: list, mark current, revoke another device.
	var list []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	}
	ali.do("GET", "/api/me/sessions", nil, 200, &list)
	if len(list) != 3 {
		t.Fatalf("sessions = %+v", list)
	}
	var phoneID string
	phone.do("GET", "/api/me/sessions", nil, 200, &list)
	for _, x := range list {
		if x.Current {
			phoneID = x.ID
		}
	}
	if phoneID == "" {
		t.Fatal("no current session")
	}
	anon.register("vali").do("DELETE", "/api/me/sessions/"+phoneID, nil, 404, nil) // not theirs
	ali.do("DELETE", "/api/me/sessions/"+phoneID, nil, 204, nil)
	phone.do("GET", "/api/me", nil, 401, nil)
	ali.do("GET", "/api/me", nil, 200, nil)

	// Disable needs the password and a second factor.
	ali.do("POST", "/api/me/2fa/disable", map[string]string{"password": "secret123", "code": "000000"}, 400, nil)
	ali.do("POST", "/api/me/2fa/disable", map[string]string{"password": "secret123", "code": codes.BackupCodes[2]}, 204, nil)
	var plain loginReply
	anon.do("POST", "/api/auth/login", creds, 200, &plain)
	if plain.MFARequired || plain.Token == "" {
		t.Fatalf("login after disable = %+v", plain)
	}
}

type notifs struct {
	Items []struct {
		Type   string `json:"type"`
		Actor  string `json:"actor"`
		PostID int    `json:"post_id"`
	} `json:"items"`
}

func (n notifs) count(typ, actor string) int {
	c := 0
	for _, x := range n.Items {
		if x.Type == typ && x.Actor == actor {
			c++
		}
	}
	return c
}

func TestBansBlocksAndMentions(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	bob := anon.register("bob")
	carol := anon.register("carol")
	dave := anon.register("dave")
	erin := anon.register("erin")

	// Ban / suspend.
	until := time.Now().Add(time.Hour).UTC()
	carol.do("PUT", "/api/admin/users/bob/ban", map[string]any{"reason": "spam"}, 403, nil)
	admin.do("PUT", "/api/admin/users/bob/ban", map[string]any{"reason": " "}, 400, nil)
	var ban struct {
		Kind   string     `json:"kind"`
		Reason string     `json:"reason"`
		Until  *time.Time `json:"until"`
	}
	admin.do("PUT", "/api/admin/users/bob/ban", map[string]any{"reason": "spam links", "until": until}, 200, &ban)
	if ban.Kind != "suspended" || ban.Reason != "spam links" || ban.Until == nil {
		t.Fatalf("ban = %+v", ban)
	}
	bob.do("GET", "/api/me", nil, 401, nil)
	anon.do("POST", "/api/auth/login", map[string]string{"login": "bob", "password": "secret123"}, 401, nil)
	admin.do("GET", "/api/admin/users/bob/ban", nil, 200, nil)
	var bans struct{ Total int }
	admin.do("GET", "/api/admin/bans", nil, 200, &bans)
	if bans.Total != 1 {
		t.Fatalf("bans = %+v", bans)
	}
	eventually(t, "suspension email", func() bool {
		for _, m := range e.app.Outbox.Sent() {
			if m.To == "bob@example.com" && m.Subject == "Your account has been suspended" {
				return true
			}
		}
		return false
	})
	admin.do("DELETE", "/api/admin/users/bob/ban", nil, 204, nil)
	admin.do("GET", "/api/admin/users/bob/ban", nil, 404, nil)
	anon.login("bob", "secret123")
	admin.do("PUT", "/api/admin/users/admin/ban", map[string]any{"reason": "x"}, 400, nil) // self

	// Block: no follows either way, no comments on the blocker's posts.
	dave.do("POST", "/api/users/carol/follow", nil, 204, nil)
	carol.do("POST", "/api/users/dave/block", nil, 204, nil)
	var followers struct{ Items []struct{ Username string } }
	carol.do("GET", "/api/users/carol/followers", nil, 200, &followers)
	if len(followers.Items) != 0 {
		t.Fatalf("block kept follow: %+v", followers)
	}
	dave.do("POST", "/api/users/carol/follow", nil, 400, nil)
	carol.do("POST", "/api/users/dave/follow", nil, 400, nil)
	var p post
	carol.do("POST", "/api/posts", map[string]any{"title": "Carol's post", "body": "hello"}, 201, &p)
	dave.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "hi"}, 400, nil)
	var blocks struct{ Items []struct{ Username string } }
	carol.do("GET", "/api/me/blocks", nil, 200, &blocks)
	if len(blocks.Items) != 1 || blocks.Items[0].Username != "dave" {
		t.Fatalf("blocks = %+v", blocks)
	}
	carol.do("DELETE", "/api/users/dave/block", nil, 204, nil)
	dave.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "hi"}, 201, nil)

	// Mentions notify on publish and in comments, once per user.
	var draft post
	erin.do("POST", "/api/posts", map[string]any{"title": "Draft", "body": "thanks @carol", "status": "draft"}, 201, &draft)
	var n notifs
	carol.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.count("mention", "erin") != 0 {
		t.Fatal("draft mentions must not notify")
	}
	erin.do("PUT", fmt.Sprintf("/api/posts/%d", draft.ID), map[string]any{"title": "Draft", "body": "thanks @carol and @nobody", "status": "published"}, 200, nil)
	erin.do("PUT", fmt.Sprintf("/api/posts/%d", draft.ID), map[string]any{"title": "Draft", "body": "thanks @Carol!"}, 200, nil)
	erin.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "cc @dave"}, 201, nil)
	carol.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.count("mention", "erin") != 1 {
		t.Fatalf("carol notifications = %+v", n)
	}
	dave.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.count("mention", "erin") != 1 {
		t.Fatalf("dave notifications = %+v", n)
	}

	// Mute silences notifications.
	dave.do("POST", "/api/users/erin/mute", nil, 204, nil)
	erin.do("POST", fmt.Sprintf("/api/posts/%d/comments", p.ID), map[string]string{"text": "again @dave"}, 201, nil)
	dave.do("GET", "/api/me/notifications", nil, 200, &n)
	if n.count("mention", "erin") != 1 {
		t.Fatalf("muted user notified: %+v", n)
	}
	dave.do("POST", "/api/users/dave/mute", nil, 400, nil)
}

func TestRevisionsSeriesAndRecommendations(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	ali := anon.register("ali")
	vali := anon.register("vali")
	sami := anon.register("sami")

	// Revisions.
	var p post
	ali.do("POST", "/api/posts", map[string]any{"title": "Go", "body": "one\ntwo", "tags": []string{"go", "k8s"}}, 201, &p)
	path := fmt.Sprintf("/api/posts/%d", p.ID)
	ali.do("PUT", path, map[string]any{"title": "Go", "body": "one\n2", "tags": []string{"go", "k8s"}}, 200, nil)
	ali.do("PUT", path, map[string]any{"title": "Go!", "body": "one\n2", "tags": []string{"go", "k8s"}}, 200, nil)
	ali.do("PUT", path, map[string]any{"title": "Go!", "body": "one\n2", "tags": []string{"go"}}, 200, nil) // text unchanged
	var revs []struct {
		Version int    `json:"version"`
		Title   string `json:"title"`
		Editor  string `json:"editor"`
	}
	ali.do("GET", path+"/revisions", nil, 200, &revs)
	if len(revs) != 2 || revs[0].Version != 2 || revs[1].Title != "Go" || revs[0].Editor != "ali" {
		t.Fatalf("revisions = %+v", revs)
	}
	vali.do("GET", path+"/revisions", nil, 403, nil)
	var rev struct {
		Body string `json:"body"`
		Diff struct {
			Body    []struct{ Op, Text string } `json:"body"`
			Added   int                         `json:"added"`
			Removed int                         `json:"removed"`
		} `json:"diff"`
	}
	ali.do("GET", path+"/revisions/1", nil, 200, &rev)
	if rev.Body != "one\ntwo" || rev.Diff.Added != 2 || rev.Diff.Removed != 2 {
		t.Fatalf("revision 1 = %+v", rev)
	}
	ali.do("GET", path+"/revisions/9", nil, 404, nil)
	var restored struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	ali.do("POST", path+"/revisions/1/restore", nil, 200, &restored)
	if restored.Title != "Go" || restored.Body != "one\ntwo" {
		t.Fatalf("restored = %+v", restored)
	}
	ali.do("GET", path+"/revisions", nil, 200, &revs)
	if len(revs) != 3 || revs[0].Title != "Go!" {
		t.Fatalf("after restore = %+v", revs)
	}

	// Series.
	var p2, draft, other post
	ali.do("POST", "/api/posts", map[string]any{"title": "Part two", "body": "x", "tags": []string{"go"}}, 201, &p2)
	ali.do("POST", "/api/posts", map[string]any{"title": "Part three", "body": "x", "status": "draft"}, 201, &draft)
	vali.do("POST", "/api/posts", map[string]any{"title": "Rust", "body": "x", "tags": []string{"rust"}}, 201, &other)
	var s struct {
		Slug  string `json:"slug"`
		Parts int    `json:"parts"`
	}
	ali.do("POST", "/api/series", map[string]string{"title": "Learn Go"}, 201, &s)
	ali.do("PUT", "/api/series/"+s.Slug+"/posts", map[string]any{"post_ids": []int{p.ID, other.ID}}, 400, nil)
	vali.do("PUT", "/api/series/"+s.Slug+"/posts", map[string]any{"post_ids": []int{other.ID}}, 403, nil)
	ali.do("PUT", "/api/series/"+s.Slug+"/posts", map[string]any{"post_ids": []int{p.ID, p2.ID, draft.ID}}, 200, nil)
	var detail struct {
		Parts int `json:"parts"`
		Posts []struct {
			Position int `json:"position"`
			PostID   int `json:"post_id"`
		} `json:"posts"`
	}
	anon.do("GET", "/api/series/"+s.Slug, nil, 200, &detail)
	if detail.Parts != 2 || len(detail.Posts) != 2 || detail.Posts[1].PostID != p2.ID {
		t.Fatalf("public series = %+v", detail)
	}
	ali.do("GET", "/api/series/"+s.Slug, nil, 200, &detail)
	if len(detail.Posts) != 3 {
		t.Fatalf("author series = %+v", detail)
	}
	var nav struct {
		Position int `json:"position"`
		Prev     *struct {
			PostID int `json:"post_id"`
		} `json:"prev"`
		Next *struct {
			PostID int `json:"post_id"`
		} `json:"next"`
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d/series", p2.ID), nil, 200, &nav)
	if nav.Position != 2 || nav.Prev == nil || nav.Prev.PostID != p.ID || nav.Next != nil {
		t.Fatalf("nav = %+v", nav)
	}
	anon.do("GET", fmt.Sprintf("/api/posts/%d/series", other.ID), nil, 404, nil)
	var mine []struct{ Slug string }
	anon.do("GET", "/api/users/ali/series", nil, 200, &mine)
	if len(mine) != 1 {
		t.Fatalf("user series = %+v", mine)
	}

	// Related posts: shared tags, not other topics.
	var related []post
	anon.do("GET", fmt.Sprintf("/api/posts/%d/related", p.ID), nil, 200, &related)
	if len(related) != 1 || related[0].ID != p2.ID {
		t.Fatalf("related = %+v", related)
	}

	// Who to follow: friends of friends first.
	sami.do("POST", "/api/users/vali/follow", nil, 204, nil)
	vali.do("POST", "/api/users/ali/follow", nil, 204, nil)
	var sugg []struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	sami.do("GET", "/api/me/suggestions/users", nil, 200, &sugg)
	if len(sugg) == 0 || sugg[0].Username != "ali" || sugg[0].Reason != "followed_by_people_you_follow" {
		t.Fatalf("suggestions = %+v", sugg)
	}
	if slices.ContainsFunc(sugg, func(x struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}) bool {
		return x.Username == "vali" || x.Username == "sami"
	}) {
		t.Fatalf("suggested followed or self: %+v", sugg)
	}
	ali.do("DELETE", "/api/series/"+s.Slug, nil, 204, nil)
	anon.do("GET", "/api/series/"+s.Slug, nil, 404, nil)
}
