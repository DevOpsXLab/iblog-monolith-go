package http

import (
	"net/http"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
)

type commentInput struct {
	Text     string `json:"text"`
	ParentID int    `json:"parent_id"` // reply to this comment (same post)
}

type commentEdit struct {
	Text string `json:"text"`
}

// listComments lists a post's comments, oldest first.
//
// Flat list with parent_id; clients build the thread. Paginate with page
// and limit, or with cursor (next_cursor). With a session, liked tells
// whether the caller liked each comment.
// Auth: optional.
//
// spector:tags comments
func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		c, err := h.Blog.ListComments(r.Context(), id, h.viewerID(r), paging(r))
		respond(w, r, http.StatusOK, c, err, postNotFound)
	}
}

// addComment comments on a post, or replies when parent_id is set.
//
// Notifies the post author and, for replies, the parent's author; pushes a
// "created" event to the comment stream. Text up to 5000 characters.
// Rate limit: 10 per minute.
// Auth: comment.create.
//
// spector:tags comments
func (h *Handler) addComment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "comment", 10, time.Minute) {
		return
	}
	id, ok := pathID(w, r)
	var in commentInput
	if ok && decode(w, r, &in) {
		c, err := h.Blog.AddComment(r.Context(), actor, id, in.ParentID, in.Text)
		respond(w, r, http.StatusCreated, c, err, postNotFound)
	}
}

// editComment edits a comment and sets edited_at.
//
// Auth: the author (ABAC policy) or comment.update.
//
// spector:tags comments
func (h *Handler) editComment(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	id, ok := pathID(w, r)
	var in commentEdit
	if ok && decode(w, r, &in) {
		c, err := h.Blog.EditComment(r.Context(), id, in.Text)
		respond(w, r, http.StatusOK, c, err, "comment not found")
	}
}

// deleteComment deletes a comment and its replies.
//
// Auth: the author (ABAC policy) or comment.delete. Audited when a
// moderator deletes someone else's comment.
//
// spector:tags comments
func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteComment(r.Context(), actor, id), "comment not found")
	}
}

// commentStream streams a post's comment events (Server-Sent Events).
//
// Events: {"event": "created"|"updated"|"deleted", "comment": {...}}.
// Auth: none.
//
// spector:tags comments
func (h *Handler) commentStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := h.Blog.GetPost(r.Context(), id, 0); err != nil {
		respond(w, r, 0, nil, err, postNotFound)
		return
	}
	h.stream(w, r, application.CommentsChannel(id))
}

// adminComments lists all comments, newest first (moderation).
//
// Auth: comment.moderate.
//
// spector:tags admin
func (h *Handler) adminComments(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		c, err := h.Blog.AllComments(r.Context(), paging(r))
		respond(w, r, http.StatusOK, c, err, "")
	}
}

// likeComment likes a comment and notifies its author.
//
// Idempotent. Rate limit: 60 per minute.
// Auth: like.create.
//
// spector:tags comments
func (h *Handler) likeComment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "like", 60, time.Minute) {
		return
	}
	if id, ok := pathID(w, r); ok {
		c, err := h.Blog.LikeComment(r.Context(), actor, id)
		respond(w, r, http.StatusOK, c, err, "comment not found")
	}
}

// unlikeComment removes the caller's like from a comment.
//
// Auth: user.
//
// spector:tags comments
func (h *Handler) unlikeComment(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		c, err := h.Blog.UnlikeComment(r.Context(), actor, id)
		respond(w, r, http.StatusOK, c, err, "comment not found")
	}
}
