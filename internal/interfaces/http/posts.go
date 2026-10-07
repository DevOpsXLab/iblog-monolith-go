package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/interfaces/http/middleware"
)

const postNotFound = "post not found"

// listPosts lists published posts, newest first.
//
// Full-text search with q (title and body), filter by tag, category and
// label, paginate with page and limit (max 50).
// Auth: none.
//
// spector:tags posts
func (h *Handler) listPosts(w http.ResponseWriter, r *http.Request) {
	cat, _ := strconv.Atoi(r.URL.Query().Get("category"))
	label, _ := strconv.Atoi(r.URL.Query().Get("label"))
	page, err := h.Blog.ListPosts(r.Context(), post.Filter{
		Query:      r.URL.Query().Get("q"),
		Tag:        r.URL.Query().Get("tag"),
		CategoryID: cat,
		LabelID:    label,
		Paging:     paging(r),
	})
	respond(w, r, http.StatusOK, page, err, "")
}

// adminPosts lists every author's posts in one status.
//
// status is draft, scheduled, published (default) or unlisted; q, tag,
// category, label, page and limit work as in GET /api/posts.
// Auth: post.read_draft.
//
// spector:tags admin
func (h *Handler) adminPosts(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		q := r.URL.Query()
		cat, _ := strconv.Atoi(q.Get("category"))
		label, _ := strconv.Atoi(q.Get("label"))
		page, err := h.Blog.AdminPosts(r.Context(), post.Filter{
			Query:      q.Get("q"),
			Tag:        q.Get("tag"),
			CategoryID: cat,
			LabelID:    label,
			Status:     post.Status(q.Get("status")),
			Paging:     paging(r),
		})
		respond(w, r, http.StatusOK, page, err, "")
	}
}

// trending lists the most viewed posts of the last days.
//
// days (1-30, default 7) and limit (max 50, default 10). Views are unique per
// user or IP.
// Auth: none.
//
// spector:tags posts
func (h *Handler) trending(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		days = 7
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		limit = 10
	}
	posts, err := h.Blog.Trending(r.Context(), days, limit)
	respond(w, r, http.StatusOK, posts, err, "")
}

// getPost returns one post.
//
// Drafts and scheduled posts are visible only to their author and
// moderators. With a token, liked and bookmarked describe the caller.
// Auth: optional.
//
// spector:tags posts
func (h *Handler) getPost(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		p, err := h.Blog.GetPost(r.Context(), id, h.viewerID(r))
		respond(w, r, http.StatusOK, p, err, postNotFound)
	}
}

// getPostBySlug returns one post by its URL slug.
//
// Auth: optional.
//
// spector:tags posts
func (h *Handler) getPostBySlug(w http.ResponseWriter, r *http.Request) {
	p, err := h.Blog.GetPostBySlug(r.Context(), r.PathValue("slug"), h.viewerID(r))
	respond(w, r, http.StatusOK, p, err, postNotFound)
}

// createPost publishes, drafts or schedules a post as the logged-in user.
//
// status is draft, scheduled (needs a future publish_at) or published
// (default). Body is Markdown; cover_url comes from POST /api/uploads.
// Followers are notified when it is published. Rate limit: 10 per minute.
// Auth: post.create.
//
// spector:tags posts
func (h *Handler) createPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "post", 10, time.Minute) {
		return
	}
	var d post.Draft
	if decode(w, r, &d) {
		p, err := h.Blog.CreatePost(r.Context(), actor, d)
		h.trackPublish(r, p, err)
		respond(w, r, http.StatusCreated, p, err, "")
	}
}

// updatePost edits a post.
//
// Fields left out of the body keep their current values; send "tags": []
// or "label_ids": [] to clear them. Moving a draft to published notifies followers; status scheduled
// (re)schedules it.
// Auth: the author (ABAC policy) or post.update.
//
// spector:tags posts
func (h *Handler) updatePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	current, err := h.Blog.GetPost(r.Context(), id, actor.ID)
	if err != nil {
		respond(w, r, 0, nil, err, postNotFound)
		return
	}
	d := current.Draft()
	if decode(w, r, &d) {
		p, err := h.Blog.UpdatePost(r.Context(), actor, id, d)
		if !current.IsPublic() {
			h.trackPublish(r, p, err)
		}
		respond(w, r, http.StatusOK, p, err, postNotFound)
	}
}

// deletePost deletes a post with its comments, likes and bookmarks.
//
// Auth: the author (ABAC policy) or post.delete. Audited.
//
// spector:tags posts
func (h *Handler) deletePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeletePost(r.Context(), actor, id), postNotFound)
	}
}

// viewPost counts a view.
//
// Unique per user (or IP when anonymous); feeds views and trending.
// Rate limit: 120 per minute.
// Auth: optional.
//
// spector:tags posts
func (h *Handler) viewPost(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "view", 120, time.Minute) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	viewer := "ip:" + h.clientIP(r)
	if uid := h.viewerID(r); uid != 0 {
		viewer = "user:" + strconv.Itoa(uid)
	}
	// A view counter must not flush every cached response.
	middleware.SkipInvalidate(r.Context())
	noContent(w, r, h.Blog.ViewPost(r.Context(), id, viewer), postNotFound)
}

// likePost likes a post and notifies its author.
//
// Idempotent: one like per user per post. Rate limit: 60 per minute.
// Auth: like.create.
//
// spector:tags likes
func (h *Handler) likePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "like", 60, time.Minute) {
		return
	}
	if id, ok := pathID(w, r); ok {
		p, err := h.Blog.LikePost(r.Context(), actor, id)
		respond(w, r, http.StatusOK, p, err, postNotFound)
	}
}

// clapPost adds claps to a post, like Medium.
//
// Body: {"count": 1-50}. A reader has at most 50 claps per post; extra claps
// are ignored. The author is notified on the first clap. Rate limit: 60 per minute.
// Auth: like.create.
//
// spector:tags likes
func (h *Handler) clapPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "like", 60, time.Minute) {
		return
	}
	id, ok := pathID(w, r)
	var in struct {
		Count int `json:"count"`
	}
	if ok && decode(w, r, &in) {
		p, err := h.Blog.ClapPost(r.Context(), actor, id, in.Count)
		respond(w, r, http.StatusOK, p, err, postNotFound)
	}
}

// listHighlights returns a post's most highlighted passages and, with a
// session, the caller's own highlights.
//
// Auth: optional.
//
// spector:tags highlights
func (h *Handler) listHighlights(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		hl, err := h.Blog.PostHighlights(r.Context(), id, h.viewerID(r))
		respond(w, r, http.StatusOK, hl, err, postNotFound)
	}
}

// addHighlight highlights a passage of a post.
//
// Body: {"start": n, "end": m}, rune offsets into the post body; the text is
// taken from the body. Rate limit: 60 per minute.
// Auth: highlight.create.
//
// spector:tags highlights
func (h *Handler) addHighlight(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "highlight", 60, time.Minute) {
		return
	}
	id, ok := pathID(w, r)
	var in struct {
		Start int `json:"start"`
		End   int `json:"end"`
	}
	if ok && decode(w, r, &in) {
		hl, err := h.Blog.AddHighlight(r.Context(), actor, id, in.Start, in.End)
		respond(w, r, http.StatusCreated, hl, err, postNotFound)
	}
}

// deleteHighlight removes a highlight.
//
// Auth: the highlight's owner or highlight.delete.
//
// spector:tags highlights
func (h *Handler) deleteHighlight(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteHighlight(r.Context(), id), "highlight not found")
	}
}

// unlikePost removes all of the caller's claps.
//
// Auth: user.
//
// spector:tags likes
func (h *Handler) unlikePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		p, err := h.Blog.UnlikePost(r.Context(), actor, id)
		respond(w, r, http.StatusOK, p, err, postNotFound)
	}
}

// bookmark saves a post to the caller's bookmarks.
//
// Idempotent. Rate limit: 60 per minute.
// Auth: bookmark.create.
//
// spector:tags bookmarks
func (h *Handler) bookmark(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "bookmark", 60, time.Minute) {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.Bookmark(r.Context(), actor, id), postNotFound)
	}
}

// unbookmark removes a post from the caller's bookmarks.
//
// Auth: user.
//
// spector:tags bookmarks
func (h *Handler) unbookmark(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.Unbookmark(r.Context(), actor, id), postNotFound)
	}
}

// listTags lists tags of published posts with counts.
//
// Auth: none.
//
// spector:tags posts
func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	t, err := h.Blog.Tags(r.Context())
	respond(w, r, http.StatusOK, t, err, "")
}

// followTag adds a tag to the caller's feed.
//
// Idempotent. Auth: follow.create.
//
// spector:tags posts
func (h *Handler) followTag(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.FollowTag(r.Context(), actor, r.PathValue("tag")), "")
	}
}

// unfollowTag removes a tag from the caller's feed.
//
// Auth: user.
//
// spector:tags posts
func (h *Handler) unfollowTag(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.UnfollowTag(r.Context(), actor, r.PathValue("tag")), "")
	}
}

// myTags lists tags the caller follows.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) myTags(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		t, err := h.Blog.FollowedTags(r.Context(), actor)
		respond(w, r, http.StatusOK, t, err, "")
	}
}
