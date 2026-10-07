package application

import (
	"context"
	"errors"
	"testing"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/revision"
)

type fakeRevisions struct {
	revision.Repository
	added []revision.Content
	err   error
}

func (r *fakeRevisions) Add(_ context.Context, _, _ int, c revision.Content) (revision.Revision, error) {
	r.added = append(r.added, c)
	return revision.Revision{}, r.err
}

// fakePosts has no UpdateWithRevision, so UpdatePost takes the
// update-then-snapshot path.
func TestUpdatePostSnapshotsWithoutAtomicRepo(t *testing.T) {
	w := newWorld(1)
	revs := &fakeRevisions{}
	w.blog.Revisions = revs
	w.posts.posts[1] = post.Post{ID: 1, UserID: 1, Title: "v1", Status: post.StatusPublished}

	if _, err := w.blog.UpdatePost(ctx, ali, 1, post.Draft{Title: "v2", Status: post.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	if len(revs.added) != 1 || revs.added[0].Title != "v1" {
		t.Fatalf("revisions = %+v, want the old text", revs.added)
	}
	// Unchanged text records nothing.
	if _, err := w.blog.UpdatePost(ctx, ali, 1, post.Draft{Title: "v2", Status: post.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	if len(revs.added) != 1 {
		t.Errorf("no-op edit recorded: %+v", revs.added)
	}
	// A failed snapshot reaches the caller.
	revs.err = errors.New("disk full")
	if _, err := w.blog.UpdatePost(ctx, ali, 1, post.Draft{Title: "v3", Status: post.StatusPublished}); err == nil {
		t.Error("snapshot error swallowed")
	}
}
