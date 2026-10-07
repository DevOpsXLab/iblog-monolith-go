package http

import (
	"net/http"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/publication"
)

const publicationNotFound = "publication not found"

// createPublication creates a team blog owned by the caller.
//
// Body: {"slug", "name", "description", "avatar_url"}. slug: 3-40 chars,
// a-z, 0-9 and -, unique (409).
// Auth: publication.create.
//
// spector:tags publications
func (h *Handler) createPublication(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in publication.PublicationInput
	if ok && decode(w, r, &in) {
		p, err := h.Blog.CreatePublication(r.Context(), actor, in)
		respond(w, r, http.StatusCreated, p, err, "")
	}
}

// getPublication returns a publication; my_role is the caller's role.
//
// Auth: optional.
//
// spector:tags publications
func (h *Handler) getPublication(w http.ResponseWriter, r *http.Request) {
	p, err := h.Blog.GetPublication(r.Context(), r.PathValue("slug"), h.viewerID(r))
	respond(w, r, http.StatusOK, p, err, publicationNotFound)
}

// updatePublication edits name, description and avatar_url (slug is fixed).
//
// Auth: publication owner or editor.
//
// spector:tags publications
func (h *Handler) updatePublication(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in publication.PublicationInput
	if ok && decode(w, r, &in) {
		p, err := h.Blog.UpdatePublication(r.Context(), actor, r.PathValue("slug"), in)
		respond(w, r, http.StatusOK, p, err, publicationNotFound)
	}
}

// deletePublication deletes a publication; its posts stay with their authors.
//
// Auth: publication owner. Audited.
//
// spector:tags publications
func (h *Handler) deletePublication(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.DeletePublication(r.Context(), actor, r.PathValue("slug")), publicationNotFound)
	}
}

// publicationPosts lists a publication's published posts, newest first.
//
// Auth: none.
//
// spector:tags publications
func (h *Handler) publicationPosts(w http.ResponseWriter, r *http.Request) {
	p, err := h.Blog.PublicationPosts(r.Context(), r.PathValue("slug"), paging(r))
	respond(w, r, http.StatusOK, p, err, publicationNotFound)
}

// publicationMembers lists members: owner, editors, then writers.
//
// Auth: none.
//
// spector:tags publications
func (h *Handler) publicationMembers(w http.ResponseWriter, r *http.Request) {
	m, err := h.Blog.PublicationMembers(r.Context(), r.PathValue("slug"))
	respond(w, r, http.StatusOK, m, err, publicationNotFound)
}

// setPublicationMember adds a member or changes their role.
//
// Body: {"role": "editor"|"writer"}. Owners manage editors and writers;
// editors manage writers.
// Auth: publication owner or editor.
//
// spector:tags publications
func (h *Handler) setPublicationMember(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in struct {
		Role publication.Role `json:"role"`
	}
	if ok && decode(w, r, &in) {
		err := h.Blog.SetPublicationMember(r.Context(), actor, r.PathValue("slug"), r.PathValue("username"), in.Role)
		noContent(w, r, err, "not found")
	}
}

// removePublicationMember removes a member, or lets the caller leave.
//
// The owner cannot be removed.
// Auth: publication owner/editor (by role), or the member themself.
//
// spector:tags publications
func (h *Handler) removePublicationMember(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		err := h.Blog.RemovePublicationMember(r.Context(), actor, r.PathValue("slug"), r.PathValue("username"))
		noContent(w, r, err, "not found")
	}
}

// myPublications lists publications the caller belongs to, with my_role.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) myPublications(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.MyPublications(r.Context(), actor)
		respond(w, r, http.StatusOK, p, err, "")
	}
}
