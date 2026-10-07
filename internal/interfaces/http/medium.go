package http

import (
	"github.com/DevOpsXLab/iblog-monolith-go/internal/interfaces/http/middleware"
	"net/http"
	"strconv"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/readinglist"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/social"
)

const listNotFound = "list not found"

type postReadInput struct {
	Progress float64 `json:"progress"` // 0-1, how far the reader scrolled
	Seconds  int     `json:"seconds"`  // time spent on the post
}

type readResult struct {
	Counted bool `json:"counted"`
}

type hideInput struct {
	Kind   social.HideKind `json:"kind"`   // post, author or tag
	Target string          `json:"target"` // post id, username or tag
}

type pinInput struct {
	PostID int `json:"post_id"`
}

type importInput struct {
	URL string `json:"url"`
}

// readPost records a read: the reader got through most of the post.
//
// Clients send it once progress (scroll, 0-1) reaches 0.6 or the reader
// spent half the reading time on the post. counted is true for the first
// qualifying read per user (or IP); the author's own reads never count.
// Rate limit: 120 per minute.
// Auth: optional.
//
// spector:tags posts
func (h *Handler) readPost(w http.ResponseWriter, r *http.Request) {
	middleware.SkipInvalidate(r.Context())
	if !h.allow(w, r, "read", 120, time.Minute) {
		return
	}
	id, ok := pathID(w, r)
	var in postReadInput
	if !ok || !decode(w, r, &in) {
		return
	}
	uid := h.viewerID(r)
	reader := "ip:" + h.clientIP(r)
	if uid != 0 {
		reader = "user:" + strconv.Itoa(uid)
	}
	counted, err := h.Blog.ReadPost(r.Context(), id, uid, reader, in.Progress, in.Seconds)
	respond(w, r, http.StatusOK, readResult{Counted: counted}, err, postNotFound)
}

// postDailyStats returns a post's views and reads per day, oldest first.
//
// ?days= 1-30 (default 30).
// Auth: whoever may edit the post.
//
// spector:tags me
func (h *Handler) postDailyStats(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if ok && ok2 {
		days, err := strconv.Atoi(r.URL.Query().Get("days"))
		if err != nil {
			days = 30
		}
		s, err := h.Blog.DailyStats(r.Context(), actor, id, days)
		respond(w, r, http.StatusOK, s, err, postNotFound)
	}
}

// forYou is the caller's ranked home feed.
//
// Ranks published posts by followed authors and tags, tags of posts the
// caller clapped, read or bookmarked, and claps, favouring recent posts.
// Leaves out own, already read and hidden posts and blocked or muted
// authors. page and limit as usual.
// Auth: user.
//
// spector:tags me
func (h *Handler) forYou(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		list, err := h.Blog.ForYou(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, list, err, "")
	}
}

// hide is "show less like this": keeps a post, author or tag out of the
// caller's feed and For You.
//
// Body: {"kind": "post"|"author"|"tag", "target": post id|username|tag}.
// Idempotent.
// Auth: user.
//
// spector:tags me
func (h *Handler) hide(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in hideInput
	if ok && decode(w, r, &in) {
		noContent(w, r, h.Blog.Hide(r.Context(), actor, in.Kind, in.Target), "not found")
	}
}

// unhide undoes a hide.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) unhide(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.Unhide(r.Context(), actor, social.HideKind(r.PathValue("kind")), r.PathValue("target")), "")
	}
}

// hidden lists what the caller hid, newest first.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) hidden(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		list, err := h.Blog.Hidden(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, list, err, "")
	}
}

// pin shows one of the caller's published posts first on their profile.
//
// Body: {"post_id": n}. Replaces the previous pin.
// Auth: user.
//
// spector:tags me
func (h *Handler) pin(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in pinInput
	if ok && decode(w, r, &in) {
		if in.PostID <= 0 {
			writeError(w, r, http.StatusBadRequest, "post_id required")
			return
		}
		noContent(w, r, h.Blog.Pin(r.Context(), actor, in.PostID), postNotFound)
	}
}

// unpin removes the caller's pinned post.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) unpin(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.Pin(r.Context(), actor, 0), "")
	}
}

// subscribe emails the caller whenever this user publishes.
//
// Needs a verified email. Idempotent. Rate limit: 30 per minute.
// Auth: user.
//
// spector:tags users
func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if ok && h.allow(w, r, "subscribe", 30, time.Minute) {
		noContent(w, r, h.Blog.Subscribe(r.Context(), actor, r.PathValue("username")), "user not found")
	}
}

// unsubscribe stops new-post emails from this user.
//
// Auth: user.
//
// spector:tags users
func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.Unsubscribe(r.Context(), actor, r.PathValue("username")), "user not found")
	}
}

// unsubscribeLink ends an email subscription from the signed link in a
// new-post email (List-Unsubscribe one-click, RFC 8058).
//
// Query: s (subscriber id), a (author id), sig. Idempotent.
// Auth: none.
//
// spector:tags users
func (h *Handler) unsubscribeLink(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s, err1 := strconv.Atoi(q.Get("s"))
	a, err2 := strconv.Atoi(q.Get("a"))
	if err1 != nil || err2 != nil {
		writeError(w, r, http.StatusBadRequest, "bad unsubscribe link")
		return
	}
	noContent(w, r, h.Blog.UnsubscribeByLink(r.Context(), s, a, q.Get("sig")), "")
}

// importPost imports a story from another site as a draft.
//
// Body: {"url": "https://..."}. The page's article becomes Markdown;
// canonical_url points back to the original. Private and internal
// addresses are refused. Rate limit: 5 per minute.
// Auth: post.create.
//
// spector:tags posts
func (h *Handler) importPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "import", 5, time.Minute) {
		return
	}
	var in importInput
	if decode(w, r, &in) {
		p, err := h.Blog.ImportPost(r.Context(), actor, in.URL)
		respond(w, r, http.StatusCreated, p, err, "")
	}
}

// createList creates a reading list.
//
// Body: {"name", "description", "private"}. At most 100 lists.
// Auth: user.
//
// spector:tags lists
func (h *Handler) createList(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in readinglist.ListInput
	if ok && decode(w, r, &in) {
		l, err := h.Blog.CreateList(r.Context(), actor, in)
		respond(w, r, http.StatusCreated, l, err, "")
	}
}

// getList returns a reading list.
//
// Private lists are visible to their owner only (404 for others).
// Auth: optional.
//
// spector:tags lists
func (h *Handler) getList(w http.ResponseWriter, r *http.Request) {
	l, err := h.Blog.GetList(r.Context(), r.PathValue("slug"), h.viewerID(r))
	respond(w, r, http.StatusOK, l, err, listNotFound)
}

// updateList changes a list's name, description and privacy.
//
// Auth: the list's owner.
//
// spector:tags lists
func (h *Handler) updateList(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in readinglist.ListInput
	if ok && decode(w, r, &in) {
		l, err := h.Blog.UpdateList(r.Context(), actor, r.PathValue("slug"), in)
		respond(w, r, http.StatusOK, l, err, listNotFound)
	}
}

// deleteList deletes a list; its posts stay.
//
// Auth: the list's owner.
//
// spector:tags lists
func (h *Handler) deleteList(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.DeleteList(r.Context(), actor, r.PathValue("slug")), listNotFound)
	}
}

// listPostsOf pages the published posts in a list, newest first.
//
// Auth: optional (private lists: owner only).
//
// spector:tags lists
func (h *Handler) listPostsOf(w http.ResponseWriter, r *http.Request) {
	p, err := h.Blog.ListItems(r.Context(), r.PathValue("slug"), h.viewerID(r), paging(r))
	respond(w, r, http.StatusOK, p, err, listNotFound)
}

// addToList saves a post to a list. Idempotent.
//
// Auth: the list's owner.
//
// spector:tags lists
func (h *Handler) addToList(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if ok && ok2 {
		noContent(w, r, h.Blog.AddToList(r.Context(), actor, r.PathValue("slug"), id), "not found")
	}
}

// removeFromList removes a post from a list.
//
// Auth: the list's owner.
//
// spector:tags lists
func (h *Handler) removeFromList(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	id, ok2 := pathID(w, r)
	if ok && ok2 {
		noContent(w, r, h.Blog.RemoveFromList(r.Context(), actor, r.PathValue("slug"), id), listNotFound)
	}
}

// myLists lists the caller's lists, private ones included.
//
// With ?post_id=, each list has contains: whether the post is in it.
// Auth: user.
//
// spector:tags lists
func (h *Handler) myLists(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		postID, _ := strconv.Atoi(r.URL.Query().Get("post_id"))
		list, err := h.Blog.MyLists(r.Context(), actor, postID, paging(r))
		respond(w, r, http.StatusOK, list, err, "")
	}
}

// userLists lists a user's public reading lists.
//
// Auth: optional.
//
// spector:tags lists
func (h *Handler) userLists(w http.ResponseWriter, r *http.Request) {
	list, err := h.Blog.UserLists(r.Context(), r.PathValue("username"), h.viewerID(r), paging(r))
	respond(w, r, http.StatusOK, list, err, "user not found")
}
