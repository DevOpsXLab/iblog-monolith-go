package http

import (
	"net/http"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

type registerInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// CaptchaToken is the Turnstile response (required when CAPTCHA is on).
	CaptchaToken string `json:"captcha_token"`
	// Website is a honeypot: hidden from people, filled in by bots.
	Website string `json:"website"`
}

type loginInput struct {
	Login    string `json:"login"` // username or email
	Password string `json:"password"`
}

type passwordChange struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type forgotInput struct {
	Email string `json:"email"`
}

type resetInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type deleteAccountInput struct {
	Password string `json:"password"`
}

type deletionScheduled struct {
	PurgeAt time.Time `json:"purge_at"`
}

// register creates an account and returns a session.
//
// Username: 3-32 letters, digits or _. Email is validated (emailx) and
// disposable addresses are refused. Password: 8-72 bytes. New accounts get
// role "user". When CAPTCHA is on (GET /api/config returns
// captcha_site_key), captcha_token must be a valid Turnstile response.
// Rate limit: 10 per minute per IP.
// Auth: none.
//
// spector:tags auth
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "register", 10, time.Minute) {
		return
	}
	var in registerInput
	if !decode(w, r, &in) || !h.humanCheck(w, r, in.CaptchaToken, in.Website) {
		return
	}
	s, err := h.Accounts.Register(r.Context(), in.Username, in.Email, in.Password, h.meta(r))
	if err == nil {
		h.Analytics.Record(r.Context(), application.EventSignup, s.User.ID, 0)
	}
	respond(w, r, http.StatusCreated, s, err, "")
}

// login returns a session for a username or email and password.
//
// Send the token as Authorization: Bearer <token>; it expires after 30
// minutes idle (renew with POST /api/auth/refresh). Logging in to an
// account with a pending deletion restores it. Rate limit: 10 per minute
// per IP; repeated failures lock the account briefly.
// Auth: none.
//
// spector:tags auth
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "login", 10, time.Minute) {
		return
	}
	var in loginInput
	if decode(w, r, &in) {
		s, err := h.Accounts.Login(r.Context(), in.Login, in.Password, h.meta(r))
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// refresh rotates the session token.
//
// The old token stops working; use the returned one.
// Auth: Bearer token.
//
// spector:tags auth
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	s, err := h.Accounts.Refresh(r.Context(), bearer(r))
	respond(w, r, http.StatusOK, s, err, "")
}

// logout ends the current session.
//
// Auth: Bearer token.
//
// spector:tags auth
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	noContent(w, r, h.Accounts.Logout(r.Context(), bearer(r), h.meta(r)), "")
}

// logoutAll ends every session of the caller.
//
// Auth: user.
//
// spector:tags auth
func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Accounts.LogoutAll(r.Context(), actor), "")
	}
}

// forgotPassword emails a password reset link.
//
// Always 202, whether or not the address exists. The link is valid for one
// hour. Rate limit: 5 per minute per IP.
// Auth: none.
//
// spector:tags auth
func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "forgot", 5, time.Minute) {
		return
	}
	var in forgotInput
	if !decode(w, r, &in) {
		return
	}
	if err := h.Accounts.ForgotPassword(r.Context(), in.Email); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// resetPassword sets a new password with the emailed token.
//
// Ends every session. The token works once.
// Auth: none.
//
// spector:tags auth
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "reset", 10, time.Minute) {
		return
	}
	var in resetInput
	if decode(w, r, &in) {
		noContent(w, r, h.Accounts.ResetPassword(r.Context(), in.Token, in.Password), "")
	}
}

// verifyEmail confirms the caller's email with the emailed code.
//
// Body: {"code": "..."}. The code works once and expires after 48 hours.
// Auth: none.
//
// spector:tags auth
func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "verify", 10, time.Minute) {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if decode(w, r, &in) {
		noContent(w, r, h.Accounts.VerifyEmail(r.Context(), in.Code), "")
	}
}

// resendVerification emails a new verification link to the caller.
//
// 400 when already verified. Rate limit: 3 per 10 minutes.
// Auth: user.
//
// spector:tags me
func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "verify-resend", 3, 10*time.Minute) {
		return
	}
	if err := h.Accounts.ResendVerification(r.Context(), actor); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// me returns the logged-in user with roles.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	if u, ok := h.requireUser(w, r); ok {
		writeJSON(w, http.StatusOK, u)
	}
}

// updateMe edits the caller's profile.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in user.ProfileUpdate
	if ok && decode(w, r, &in) {
		u, err := h.Accounts.UpdateProfile(r.Context(), actor, in)
		respond(w, r, http.StatusOK, u, err, "")
	}
}

// deleteMe deletes the caller's account after a 30-day grace period.
//
// Needs the current password. Every session ends now; logging in again
// within 30 days restores the account, otherwise it is purged with all its
// posts and comments.
// Auth: user.
//
// spector:tags me
func (h *Handler) deleteMe(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in deleteAccountInput
	if ok && decode(w, r, &in) {
		at, err := h.Accounts.DeleteAccount(r.Context(), actor, in.Password)
		respond(w, r, http.StatusAccepted, deletionScheduled{PurgeAt: at}, err, "")
	}
}

// changePassword changes the password and returns a fresh session.
//
// Every other session ends.
// Auth: user.
//
// spector:tags me
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in passwordChange
	if ok && decode(w, r, &in) {
		s, err := h.Accounts.ChangePassword(r.Context(), actor, in.OldPassword, in.NewPassword, h.meta(r))
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// myPosts lists the caller's posts by status.
//
// status: published (default), draft or scheduled.
// Auth: user.
//
// spector:tags me
func (h *Handler) myPosts(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.MyPosts(r.Context(), actor, post.Status(r.URL.Query().Get("status")), paging(r))
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// myStats lists the caller's posts with views, claps, comments, bookmarks
// and highlights, newest first, plus totals for the page.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) myStats(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		s, err := h.Blog.MyStats(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// feed lists posts from authors the caller follows.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) feed(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.Feed(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// myBookmarks lists the caller's bookmarked posts.
//
// Auth: user.
//
// spector:tags bookmarks
func (h *Handler) myBookmarks(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.Bookmarks(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// profile returns a public profile.
//
// With a token, is_following describes the caller.
// Auth: optional.
//
// spector:tags users
func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	p, err := h.Accounts.Profile(r.Context(), r.PathValue("username"), h.viewerID(r))
	respond(w, r, http.StatusOK, p, err, "user not found")
}

// userPosts lists a user's published posts.
//
// Auth: none.
//
// spector:tags users
func (h *Handler) userPosts(w http.ResponseWriter, r *http.Request) {
	p, err := h.Accounts.UserPosts(r.Context(), r.PathValue("username"), paging(r))
	respond(w, r, http.StatusOK, p, err, "user not found")
}

// followers lists who follows a user.
//
// Auth: none.
//
// spector:tags users
func (h *Handler) followers(w http.ResponseWriter, r *http.Request) {
	p, err := h.Accounts.Followers(r.Context(), r.PathValue("username"), paging(r))
	respond(w, r, http.StatusOK, p, err, "user not found")
}

// following lists who a user follows.
//
// Auth: none.
//
// spector:tags users
func (h *Handler) following(w http.ResponseWriter, r *http.Request) {
	p, err := h.Accounts.Following(r.Context(), r.PathValue("username"), paging(r))
	respond(w, r, http.StatusOK, p, err, "user not found")
}

// follow follows a user and notifies them.
//
// Idempotent. Rate limit: 60 per minute.
// Auth: follow.create.
//
// spector:tags users
func (h *Handler) follow(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if ok && h.allow(w, r, "follow", 60, time.Minute) {
		noContent(w, r, h.Accounts.Follow(r.Context(), actor, r.PathValue("username")), "user not found")
	}
}

// unfollow stops following a user.
//
// Auth: user.
//
// spector:tags users
func (h *Handler) unfollow(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Accounts.Unfollow(r.Context(), actor, r.PathValue("username")), "user not found")
	}
}
