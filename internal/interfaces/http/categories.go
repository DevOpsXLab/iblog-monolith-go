package http

import (
	"net/http"
	"strconv"
)

type categoryInput struct {
	Name string `json:"name"`
}

type labelInput struct {
	Name  string `json:"name"`
	Color string `json:"color"` // #rrggbb, default grey
}

const categoryNotFound = "category not found"

// listCategories lists categories with post counts.
//
// Auth: none.
//
// spector:tags categories
func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	c, err := h.Blog.ListCategories(r.Context())
	respond(w, r, http.StatusOK, c, err, "")
}

// createCategory creates a category.
//
// Names are unique, case-insensitive. Audited.
// Auth: category.write (admin).
//
// spector:tags categories
func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in categoryInput
	if ok && decode(w, r, &in) {
		c, err := h.Blog.CreateCategory(r.Context(), actor, in.Name)
		respond(w, r, http.StatusCreated, c, err, "")
	}
}

// deleteCategory deletes a category; its posts stay, uncategorized.
//
// Audited.
// Auth: category.write (admin).
//
// spector:tags categories
func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteCategory(r.Context(), actor, id), categoryNotFound)
	}
}

// categoryPosts lists published posts in a category.
//
// Paginate with page and limit.
// Auth: none.
//
// spector:tags categories
func (h *Handler) categoryPosts(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		p, err := h.Blog.CategoryPosts(r.Context(), id, paging(r))
		respond(w, r, http.StatusOK, p, err, categoryNotFound)
	}
}

// listLabels lists labels.
//
// Labels are curated and coloured; set them on a post with label_ids, filter
// with GET /api/posts?label=.
// Auth: none.
//
// spector:tags labels
func (h *Handler) listLabels(w http.ResponseWriter, r *http.Request) {
	l, err := h.Blog.Labels(r.Context())
	respond(w, r, http.StatusOK, l, err, "")
}

// createLabel creates a label.
//
// Audited.
// Auth: label.write (admin).
//
// spector:tags labels
func (h *Handler) createLabel(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in labelInput
	if ok && decode(w, r, &in) {
		l, err := h.Blog.CreateLabel(r.Context(), actor, in.Name, in.Color)
		respond(w, r, http.StatusCreated, l, err, "")
	}
}

// deleteLabel deletes a label and removes it from posts.
//
// Audited.
// Auth: label.write (admin).
//
// spector:tags labels
func (h *Handler) deleteLabel(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteLabel(r.Context(), actor, id), "label not found")
	}
}

// stats returns site totals.
//
// Auth: stats.read (admin).
//
// spector:tags admin
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		s, err := h.Blog.Stats(r.Context())
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// adminUsers lists users, searchable by username or email.
//
// Roles, bans and sessions are managed through /api/guard/users/{id}/...
// Auth: user.read.
//
// spector:tags admin
func (h *Handler) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		u, err := h.Accounts.ListUsers(r.Context(), r.URL.Query().Get("q"), paging(r))
		respond(w, r, http.StatusOK, u, err, "")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
