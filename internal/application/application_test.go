package application

// Use-case tests with in-memory fakes: no database, no Guard. Each fake
// embeds its interface and overrides only what the test exercises, so an
// unexpected call panics instead of passing silently.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/comment"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/social"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

// fakeAuthz allows owners (ABAC) and the listed permissions (RBAC).
type fakeAuthz struct {
	caller int
	perms  map[string]bool
}

func (a fakeAuthz) Can(_ context.Context, resource, action string, _, ownerID int) error {
	switch {
	case a.caller == 0:
		return domain.ErrUnauthorized
	case ownerID != 0 && ownerID == a.caller, a.perms[resource+"."+action]:
		return nil
	}
	return domain.ErrForbidden
}

type fakeAudit struct{ actions []string }

func (a *fakeAudit) Record(_ context.Context, _ int, action, _ string, _ map[string]any) {
	a.actions = append(a.actions, action)
}

type fakeJobs struct {
	Jobs
	published []int
	fanout    []int
	purges    []time.Time
	mail      []Email
}

func (j *fakeJobs) PublishPostAt(_ context.Context, id int, _ time.Time) error {
	j.published = append(j.published, id)
	return nil
}
func (j *fakeJobs) FanoutNewPost(_ context.Context, id int) error {
	j.fanout = append(j.fanout, id)
	return nil
}
func (j *fakeJobs) PurgeAccountAt(_ context.Context, _ int, at time.Time) error {
	j.purges = append(j.purges, at)
	return nil
}
func (j *fakeJobs) SendEmailOnce(_ context.Context, _ string, m Email) error {
	j.mail = append(j.mail, m)
	return nil
}
func (j *fakeJobs) SendEmail(_ context.Context, m Email) error {
	j.mail = append(j.mail, m)
	return nil
}

type fakeEvents struct{ Events }

func (fakeEvents) Publish(context.Context, string, any) error { return nil }

type fakeViews struct{ ViewCounter }

func (fakeViews) Count(context.Context, int) int64 { return 7 }

type fakePosts struct {
	post.Repository
	posts map[int]post.Post
}

func (r *fakePosts) Get(_ context.Context, id, _ int) (post.Post, error) {
	p, ok := r.posts[id]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, nil
}

func (r *fakePosts) Create(_ context.Context, d post.Draft, authorID int, author string) (post.Post, error) {
	p := post.Post{ID: len(r.posts) + 1, Title: d.Title, Status: d.Status, PublishAt: d.PublishAt, UserID: authorID, Author: author}
	r.posts[p.ID] = p
	return p, nil
}

func (r *fakePosts) Update(_ context.Context, id int, d post.Draft) (post.Post, error) {
	p := r.posts[id]
	p.Title, p.Status, p.PublishAt = d.Title, d.Status, d.PublishAt
	r.posts[id] = p
	return p, nil
}

func (r *fakePosts) Delete(_ context.Context, id int) error {
	delete(r.posts, id)
	return nil
}

type fakeComments struct {
	comment.Repository
	byID map[int]comment.Comment
}

func (r *fakeComments) Get(_ context.Context, id int) (comment.Comment, error) {
	c, ok := r.byID[id]
	if !ok {
		return c, domain.ErrNotFound
	}
	return c, nil
}

func (r *fakeComments) Add(_ context.Context, c comment.Comment) (comment.Comment, error) {
	c.ID = len(r.byID) + 1
	r.byID[c.ID] = c
	return c, nil
}

type fakeSocial struct {
	social.Repository
	sent []social.Notification
}

func (s *fakeSocial) Notify(_ context.Context, n social.Notification, to []int) ([]social.Notification, error) {
	var out []social.Notification
	for _, id := range to {
		n.UserID = id
		out = append(out, n)
	}
	s.sent = append(s.sent, out...)
	return out, nil
}

type world struct {
	blog     *Blog
	posts    *fakePosts
	comments *fakeComments
	social   *fakeSocial
	jobs     *fakeJobs
	audit    *fakeAudit
}

func newWorld(caller int, perms ...string) *world {
	w := &world{
		posts:    &fakePosts{posts: map[int]post.Post{}},
		comments: &fakeComments{byID: map[int]comment.Comment{}},
		social:   &fakeSocial{},
		jobs:     &fakeJobs{},
		audit:    &fakeAudit{},
	}
	p := map[string]bool{}
	for _, c := range perms {
		p[c] = true
	}
	w.blog = NewBlog(Repos{Posts: w.posts, Comments: w.comments, Social: w.social},
		Ports{Authz: fakeAuthz{caller: caller, perms: p}, Audit: w.audit, Jobs: w.jobs, Events: fakeEvents{}, Views: fakeViews{}})
	w.blog.now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	return w
}

var (
	ali  = user.User{ID: 1, Username: "ali"}
	vali = user.User{ID: 2, Username: "vali"}
	ctx  = context.Background()
)

func TestCreatePostNeedsPermission(t *testing.T) {
	w := newWorld(1) // no post.create
	if _, err := w.blog.CreatePost(ctx, ali, post.Draft{Title: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestPublishingSchedulesOrFansOut(t *testing.T) {
	w := newWorld(1, "post.create")
	at := w.blog.now().Add(time.Hour)

	p, err := w.blog.CreatePost(ctx, ali, post.Draft{Title: "later", Status: post.StatusScheduled, PublishAt: &at})
	if err != nil || len(w.jobs.published) != 1 || len(w.jobs.fanout) != 0 {
		t.Fatalf("scheduled: %v published=%v fanout=%v", err, w.jobs.published, w.jobs.fanout)
	}

	// Draft -> published announces once.
	d, _ := w.blog.CreatePost(ctx, ali, post.Draft{Title: "draft", Status: post.StatusDraft})
	if len(w.jobs.fanout) != 0 {
		t.Fatal("draft must not fan out")
	}
	if _, err := w.blog.UpdatePost(ctx, ali, d.ID, post.Draft{Title: "draft", Status: post.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.blog.UpdatePost(ctx, ali, d.ID, post.Draft{Title: "draft v2", Status: post.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	if len(w.jobs.fanout) != 1 || w.jobs.fanout[0] != d.ID {
		t.Errorf("fanout = %v, want [%d]", w.jobs.fanout, d.ID)
	}
	_ = p
}

func TestOwnershipABAC(t *testing.T) {
	w := newWorld(2) // vali, no moderator permissions
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusPublished}
	if _, err := w.blog.UpdatePost(ctx, vali, 1, post.Draft{Title: "hacked"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("update other's post: %v", err)
	}
	if err := w.blog.DeletePost(ctx, vali, 1); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("delete other's post: %v", err)
	}

	mod := newWorld(2, "post.delete")
	mod.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusPublished}
	if err := mod.blog.DeletePost(ctx, vali, 1); err != nil {
		t.Fatalf("moderator delete: %v", err)
	}
	if len(mod.audit.actions) != 1 || mod.audit.actions[0] != "post.delete" {
		t.Errorf("audit = %v", mod.audit.actions)
	}
}

func TestDraftsHiddenFromOthers(t *testing.T) {
	w := newWorld(2)
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusDraft}
	if _, err := w.blog.GetPost(ctx, 1, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other user sees draft: %v", err)
	}
	owner := newWorld(1)
	owner.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusDraft}
	p, err := owner.blog.GetPost(ctx, 1, 1)
	if err != nil || p.Views != 7 {
		t.Errorf("owner: %+v %v", p, err)
	}
}

func TestReplyNotifiesPostAndParentAuthors(t *testing.T) {
	w := newWorld(3, "comment.create")
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusPublished}
	w.comments.byID[1] = comment.Comment{ID: 1, PostID: 1, UserID: 2}
	carol := user.User{ID: 3, Username: "carol"}
	if _, err := w.blog.AddComment(ctx, carol, 1, 1, "agree"); err != nil {
		t.Fatal(err)
	}
	got := map[social.NotificationType]int{}
	for _, n := range w.social.sent {
		got[n.Type] = n.UserID
	}
	if got[social.NotifyComment] != 1 || got[social.NotifyReply] != 2 {
		t.Errorf("notifications = %+v", w.social.sent)
	}
}

func TestNoSelfNotification(t *testing.T) {
	w := newWorld(1, "comment.create")
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusPublished}
	if _, err := w.blog.AddComment(ctx, ali, 1, 0, "note to self"); err != nil {
		t.Fatal(err)
	}
	if len(w.social.sent) != 0 {
		t.Errorf("self notification sent: %+v", w.social.sent)
	}
}

// Accounts

type fakeUsers struct {
	user.Repository
	u       user.User
	deleted *time.Time
}

func (r *fakeUsers) GetByLogin(context.Context, string) (user.User, error) { return r.u, nil }
func (r *fakeUsers) MarkDeleted(_ context.Context, _ int, at time.Time) error {
	r.deleted = &at
	return nil
}

type fakeIdentity struct {
	Identity
	password  string
	loggedOut bool
}

func (i *fakeIdentity) VerifyPassword(_ context.Context, _, p string) error {
	if p != i.password {
		return ErrInvalidCredentials
	}
	return nil
}
func (i *fakeIdentity) LogoutAll(context.Context, int) error {
	i.loggedOut = true
	return nil
}

type fakeResets struct{ ResetTokens }

func (fakeResets) Issue(context.Context, int, time.Duration) (string, error) { return "tok123", nil }

func TestDeleteAccountGracePeriod(t *testing.T) {
	w := newWorld(1)
	users := &fakeUsers{u: ali}
	id := &fakeIdentity{password: "secret123"}
	a := NewAccounts(users, id, fakeResets{}, fakeResets{}, w.blog, "http://site")
	a.now = w.blog.now

	if _, err := a.DeleteAccount(ctx, ali, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	at, err := a.DeleteAccount(ctx, ali, "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if want := a.now().Add(30 * 24 * time.Hour); !at.Equal(want) || len(w.jobs.purges) != 1 || !w.jobs.purges[0].Equal(want) {
		t.Errorf("purge at %v, jobs %v", at, w.jobs.purges)
	}
	if users.deleted == nil || !id.loggedOut {
		t.Error("account not marked or sessions not ended")
	}
}

func TestForgotPasswordSendsLink(t *testing.T) {
	w := newWorld(0)
	a := NewAccounts(&fakeUsers{u: user.User{ID: 1, Username: "ali", Email: "ali@example.com"}}, &fakeIdentity{}, fakeResets{}, fakeResets{}, w.blog, "http://site")
	if err := a.ForgotPassword(ctx, "ali@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(w.jobs.mail) != 1 || w.jobs.mail[0].To != "ali@example.com" {
		t.Fatalf("mail = %+v", w.jobs.mail)
	}
	if want := "http://site/reset-password?token=tok123"; !contains(w.jobs.mail[0].Text, want) {
		t.Errorf("mail text lacks %q", want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
