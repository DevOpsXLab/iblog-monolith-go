package application

// Periodic sweep jobs (PublishDue, PurgeDue, LiftExpired) with fakes.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/sanction"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

// schedPosts publishes ids listed in due; Publish fails for ids in failing.
type schedPosts struct {
	*fakePosts
	due     []int
	failing map[int]bool
	dueAt   time.Time
}

func (r *schedPosts) DueScheduled(_ context.Context, now time.Time) ([]int, error) {
	r.dueAt = now
	return r.due, nil
}

func (r *schedPosts) Publish(_ context.Context, id int, now time.Time) (bool, error) {
	if r.failing[id] {
		return false, errors.New("db down")
	}
	p, ok := r.posts[id]
	if !ok {
		return false, nil
	}
	if p.Status != post.StatusScheduled || p.PublishAt.After(now) {
		return false, nil
	}
	p.Status = post.StatusPublished
	r.posts[id] = p
	return true, nil
}

func TestPublishDue(t *testing.T) {
	w := newWorld(0)
	past := w.blog.now().Add(-time.Minute)
	posts := &schedPosts{fakePosts: w.posts, due: []int{1, 2, 3}, failing: map[int]bool{3: true}}
	w.posts.posts[1] = post.Post{ID: 1, Status: post.StatusScheduled, PublishAt: &past}
	// 2 was published by an earlier attempt whose fan-out failed: enqueue again.
	w.posts.posts[2] = post.Post{ID: 2, Status: post.StatusPublished}
	w.blog.Posts = posts

	err := w.blog.PublishDue(ctx)
	if err == nil || !contains(err.Error(), "db down") {
		t.Fatalf("err = %v, want joined db error", err)
	}
	if !posts.dueAt.Equal(w.blog.now()) {
		t.Errorf("DueScheduled(now=%v), want %v", posts.dueAt, w.blog.now())
	}
	if w.posts.posts[1].Status != post.StatusPublished {
		t.Errorf("post 1 = %s", w.posts.posts[1].Status)
	}
	if !slices.Equal(w.jobs.fanout, []int{1, 2}) {
		t.Errorf("fanout = %v, want [1 2]", w.jobs.fanout)
	}
	if !slices.Equal(w.audit.actions, []string{"post.publish_scheduled"}) {
		t.Errorf("audit = %v (only the flipped post)", w.audit.actions)
	}
}

func TestPublishScheduledSkipsMissingAndDrafts(t *testing.T) {
	w := newWorld(0)
	w.posts.posts[1] = post.Post{ID: 1, Status: post.StatusDraft}
	w.blog.Posts = &schedPosts{fakePosts: w.posts}
	for _, id := range []int{1, 99} {
		if err := w.blog.PublishScheduled(ctx, id); err != nil {
			t.Errorf("post %d: %v", id, err)
		}
	}
	if len(w.jobs.fanout) != 0 {
		t.Errorf("fanout = %v", w.jobs.fanout)
	}
}

// purgeUsers records the cutoffs it gets; Purge reports removed for ids in gone.
type purgeUsers struct {
	user.Repository
	due     []int
	gone    map[int]bool
	fail    map[int]bool
	cutoffs []time.Time
	purged  []int
}

func (r *purgeUsers) DuePurges(_ context.Context, cutoff time.Time) ([]int, error) {
	r.cutoffs = append(r.cutoffs, cutoff)
	return r.due, nil
}

func (r *purgeUsers) Purge(_ context.Context, id int, cutoff time.Time) (bool, error) {
	r.cutoffs = append(r.cutoffs, cutoff)
	if r.fail[id] {
		return false, errors.New("purge failed")
	}
	if r.gone[id] {
		r.purged = append(r.purged, id)
		return true, nil
	}
	return false, nil
}

func TestPurgeDue(t *testing.T) {
	w := newWorld(0)
	users := &purgeUsers{due: []int{1, 2, 3}, gone: map[int]bool{1: true}, fail: map[int]bool{3: true}}
	a := NewAccounts(users, &fakeIdentity{}, fakeResets{}, fakeResets{}, w.blog, "")
	a.now = w.blog.now

	err := a.PurgeDue(ctx)
	if err == nil || !contains(err.Error(), "purge failed") {
		t.Fatalf("err = %v", err)
	}
	want := w.blog.now().Add(-user.DeletionGrace)
	for _, c := range users.cutoffs {
		if !c.Equal(want) {
			t.Errorf("cutoff %v, want %v (now - grace)", c, want)
		}
	}
	if len(users.cutoffs) != 4 {
		t.Errorf("calls = %d, want DuePurges + 3 Purge", len(users.cutoffs))
	}
	// Restored in time (2) and failed (3) are not audited.
	if !slices.Equal(users.purged, []int{1}) || !slices.Equal(w.audit.actions, []string{"account.purge"}) {
		t.Errorf("purged = %v audit = %v", users.purged, w.audit.actions)
	}
}

type fakeSanctions struct {
	sanction.Repository
	expired []int
	deleted []int
	at      time.Time
}

func (s *fakeSanctions) Expired(_ context.Context, now time.Time) ([]int, error) {
	s.at = now
	return s.expired, nil
}

func (s *fakeSanctions) Delete(_ context.Context, id int) error {
	s.deleted = append(s.deleted, id)
	if id == 2 {
		return domain.ErrNotFound // already lifted: fine
	}
	return nil
}

type blockIdentity struct {
	fakeIdentity
	unblocked []int
	fail      int
}

func (i *blockIdentity) SetBlocked(_ context.Context, _, id int, blocked bool) error {
	if blocked {
		return errors.New("unexpected block")
	}
	if id == i.fail {
		return errors.New("guard down")
	}
	i.unblocked = append(i.unblocked, id)
	return nil
}

func TestLiftExpired(t *testing.T) {
	w := newWorld(0)
	id := &blockIdentity{fail: 3}
	a := NewAccounts(&fakeUsers{}, id, fakeResets{}, fakeResets{}, w.blog, "")
	a.now = w.blog.now

	// Without a sanctions repository the job is a no-op.
	if err := a.LiftExpired(ctx); err != nil {
		t.Fatal(err)
	}

	s := &fakeSanctions{expired: []int{1, 2, 3}}
	a.Sanctions = s
	err := a.LiftExpired(ctx)
	if err == nil || !contains(err.Error(), "guard down") {
		t.Fatalf("err = %v", err)
	}
	if !s.at.Equal(w.blog.now()) {
		t.Errorf("Expired(%v)", s.at)
	}
	// 3 stays sanctioned when Guard fails, so the next sweep retries it.
	if !slices.Equal(id.unblocked, []int{1, 2}) || !slices.Equal(s.deleted, []int{1, 2}) {
		t.Errorf("unblocked = %v deleted = %v", id.unblocked, s.deleted)
	}
	if len(w.audit.actions) != 2 {
		t.Errorf("audit = %v", w.audit.actions)
	}
}
