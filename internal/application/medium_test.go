package application

import (
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
)

func TestPinOwnPublishedOnly(t *testing.T) {
	w := newWorld(1)
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusDraft}
	w.posts.posts[2] = post.Post{ID: 2, UserID: 2, Status: post.StatusPublished}
	for _, id := range []int{1, 2} {
		if err := w.blog.Pin(ctx, ali, id); err == nil {
			t.Errorf("pinned post %d", id)
		}
	}
}

func TestReadSkipsAuthorAndShortVisits(t *testing.T) {
	w := newWorld(1)
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Status: post.StatusUnlisted, ReadingTime: 4}
	w.posts.posts[2] = post.Post{ID: 2, UserID: 1, Status: post.StatusDraft}
	if ok, err := w.blog.ReadPost(ctx, 1, 1, "user:1", 1, 0); ok || err != nil {
		t.Errorf("author read counted: %v %v", ok, err)
	}
	if ok, err := w.blog.ReadPost(ctx, 1, 2, "user:2", 0.3, 60); ok || err != nil {
		t.Errorf("short visit counted: %v %v", ok, err)
	}
	if _, err := w.blog.ReadPost(ctx, 2, 2, "user:2", 1, 0); err == nil {
		t.Error("draft read")
	}
}
