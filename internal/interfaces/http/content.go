package http

import (
	"net/http"
	"strconv"

	"github.com/iBlog/iblog-monolith-go/internal/domain/series"
)

type seriesPostsInput struct {
	PostIDs []int `json:"post_ids"` // in reading order
}

func pathVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v <= 0 {
		writeError(w, r, http.StatusBadRequest, "bad version")
		return 0, false
	}
	return v, true
}

func limitParam(r *http.Request, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		return n
	}
	return def
}

// listRevisions lists a post's earlier versions, newest first.
//
// Every edit that changes the title, subtitle or body keeps the previous
// text as a revision. Bodies are left out; fetch one revision for its body
// and diff.
// Auth: whoever may edit the post (author, post.update, publication editor).
//
// spector:tags revisions
func (h *Handler) listRevisions(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if ok && ok2 {
		list, err := h.Blog.PostRevisions(r.Context(), actor, id)
		respond(w, r, http.StatusOK, list, err, "post not found")
	}
}

// getRevision returns a revision with a line diff against the current text.
//
// diff.title, diff.subtitle and diff.body are lists of {op, text} where op
// is "=" (unchanged), "-" (only in the revision) or "+" (only in the
// current text).
// Auth: whoever may edit the post.
//
// spector:tags revisions
func (h *Handler) getRevision(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if !ok || !ok2 {
		return
	}
	if v, ok := pathVersion(w, r); ok {
		d, err := h.Blog.PostRevision(r.Context(), actor, id, v)
		respond(w, r, http.StatusOK, d, err, "revision not found")
	}
}

// restoreRevision makes an earlier version the current text.
//
// The text it replaces becomes a new revision, so a restore can be undone.
// Status, tags and other fields stay as they are.
// Auth: whoever may edit the post.
//
// spector:tags revisions
func (h *Handler) restoreRevision(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if !ok || !ok2 {
		return
	}
	if v, ok := pathVersion(w, r); ok {
		p, err := h.Blog.RestoreRevision(r.Context(), actor, id, v)
		respond(w, r, http.StatusOK, p, err, "revision not found")
	}
}

// createSeries creates an empty series owned by the caller.
//
// Add posts with PUT /api/series/{slug}/posts.
// Auth: post.create.
//
// spector:tags series
func (h *Handler) createSeries(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in series.SeriesInput
	if ok && decode(w, r, &in) {
		s, err := h.Blog.CreateSeries(r.Context(), actor, in)
		respond(w, r, http.StatusCreated, s, err, "")
	}
}

// getSeries returns a series with its posts in order.
//
// Readers see published parts; the author also sees drafts.
// Auth: optional.
//
// spector:tags series
func (h *Handler) getSeries(w http.ResponseWriter, r *http.Request) {
	d, err := h.Blog.GetSeries(r.Context(), r.PathValue("slug"), h.viewerID(r))
	respond(w, r, http.StatusOK, d, err, "series not found")
}

// updateSeries changes a series' title and description.
//
// Auth: series author or post.update.
//
// spector:tags series
func (h *Handler) updateSeries(w http.ResponseWriter, r *http.Request) {
	_, ok := h.requireUser(w, r)
	var in series.SeriesInput
	if ok && decode(w, r, &in) {
		s, err := h.Blog.UpdateSeries(r.Context(), r.PathValue("slug"), in)
		respond(w, r, http.StatusOK, s, err, "series not found")
	}
}

// deleteSeries deletes a series; its posts stay.
//
// Auth: series author or post.update.
//
// spector:tags series
func (h *Handler) deleteSeries(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.DeleteSeries(r.Context(), r.PathValue("slug")), "series not found")
	}
}

// setSeriesPosts replaces a series' posts, in reading order.
//
// Posts must be the series author's and not in another series. Send an
// empty list to clear it. At most 100 posts.
// Auth: series author or post.update.
//
// spector:tags series
func (h *Handler) setSeriesPosts(w http.ResponseWriter, r *http.Request) {
	_, ok := h.requireUser(w, r)
	var in seriesPostsInput
	if ok && decode(w, r, &in) {
		d, err := h.Blog.SetSeriesPosts(r.Context(), r.PathValue("slug"), in.PostIDs)
		respond(w, r, http.StatusOK, d, err, "series not found")
	}
}

// userSeries lists a user's series, newest first.
//
// Auth: none.
//
// spector:tags series
func (h *Handler) userSeries(w http.ResponseWriter, r *http.Request) {
	list, err := h.Blog.UserSeries(r.Context(), r.PathValue("username"))
	respond(w, r, http.StatusOK, list, err, "user not found")
}

// postSeries returns a post's place in its series: position, previous and
// next parts and the full list.
//
// 404 when the post is in no series.
// Auth: optional.
//
// spector:tags series
func (h *Handler) postSeries(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		n, err := h.Blog.PostSeries(r.Context(), id, h.viewerID(r))
		respond(w, r, http.StatusOK, n, err, "not in a series")
	}
}

// relatedPosts lists published posts similar to this one.
//
// Ranked by shared tags, then same category and author, newest first on
// ties. ?limit= 1-20 (default 5).
// Auth: optional.
//
// spector:tags posts
func (h *Handler) relatedPosts(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		list, err := h.Blog.RelatedPosts(r.Context(), id, h.viewerID(r), limitParam(r, 5))
		respond(w, r, http.StatusOK, list, err, "post not found")
	}
}

// suggestedUsers is who to follow.
//
// Ranked by how many people you follow follow them, then by their posts in
// tags you follow, then by followers. Leaves out you, people you follow,
// blocked, muted and banned users. reason says why each was suggested.
// ?limit= 1-50 (default 10).
// Auth: user.
//
// spector:tags users
func (h *Handler) suggestedUsers(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		list, err := h.Blog.SuggestUsers(r.Context(), actor, limitParam(r, 10))
		respond(w, r, http.StatusOK, list, err, "")
	}
}
