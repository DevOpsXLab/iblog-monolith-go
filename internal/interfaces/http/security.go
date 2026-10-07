package http

import (
	"net/http"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/relation"
)

type mfaLoginInput struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"` // 6-digit app code or a backup code
}

type mfaCodeInput struct {
	Code string `json:"code"`
}

type mfaDisableInput struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

type backupCodes struct {
	BackupCodes []string `json:"backup_codes"`
}

type sanctionInput struct {
	Reason string     `json:"reason"`
	Until  *time.Time `json:"until"` // empty = permanent ban
}

// loginMFA finishes a login for an account with two-factor authentication.
//
// POST /api/auth/login answers {"mfa_required": true, "mfa_token": "..."}
// for such accounts; send that token with a 6-digit code from the
// authenticator app or a backup code. The token lives 5 minutes and allows
// 5 guesses; at most 10 guesses per account per 15 minutes. Rate limit: 10
// per minute per IP.
// Auth: none.
//
// spector:tags auth
func (h *Handler) loginMFA(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "login-mfa", 10, time.Minute) {
		return
	}
	var in mfaLoginInput
	if decode(w, r, &in) {
		s, err := h.Accounts.LoginMFA(r.Context(), in.MFAToken, in.Code, h.meta(r))
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// mfaStatus tells whether two-factor authentication is on and how many
// backup codes are left.
//
// Auth: user.
//
// spector:tags 2fa
func (h *Handler) mfaStatus(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		s, err := h.Accounts.MFAStatus(r.Context(), actor)
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// mfaSetup starts two-factor setup and returns a new secret.
//
// Show otpauth_url as a QR code (or the secret for manual entry), then
// confirm with POST /api/me/2fa/enable. Calling it again replaces a pending
// setup. 400 when 2FA is already on.
// Auth: user.
//
// spector:tags 2fa
func (h *Handler) mfaSetup(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		s, err := h.Accounts.SetupMFA(r.Context(), actor)
		respond(w, r, http.StatusOK, s, err, "")
	}
}

// mfaEnable turns two-factor authentication on with a code from the app.
//
// Returns 10 single-use backup codes; they are shown only once.
// Auth: user.
//
// spector:tags 2fa
func (h *Handler) mfaEnable(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "mfa", 10, time.Minute) {
		return
	}
	actor, ok := h.requireUser(w, r)
	var in mfaCodeInput
	if ok && decode(w, r, &in) {
		codes, err := h.Accounts.EnableMFA(r.Context(), actor, in.Code)
		respond(w, r, http.StatusOK, backupCodes{codes}, err, "")
	}
}

// mfaDisable turns two-factor authentication off.
//
// Needs the password and a current code or a backup code.
// Auth: user.
//
// spector:tags 2fa
func (h *Handler) mfaDisable(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "mfa", 10, time.Minute) {
		return
	}
	actor, ok := h.requireUser(w, r)
	var in mfaDisableInput
	if ok && decode(w, r, &in) {
		noContent(w, r, h.Accounts.DisableMFA(r.Context(), actor, in.Password, in.Code), "")
	}
}

// mfaBackupCodes replaces every backup code with 10 new ones.
//
// Needs a current code (or an unused backup code).
// Auth: user.
//
// spector:tags 2fa
func (h *Handler) mfaBackupCodes(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "mfa", 10, time.Minute) {
		return
	}
	actor, ok := h.requireUser(w, r)
	var in mfaCodeInput
	if ok && decode(w, r, &in) {
		codes, err := h.Accounts.RegenerateBackupCodes(r.Context(), actor, in.Code)
		respond(w, r, http.StatusOK, backupCodes{codes}, err, "")
	}
}

// mySessions lists the caller's logged-in devices.
//
// Most recently used first; current marks the session making this request.
// Auth: user.
//
// spector:tags me
func (h *Handler) mySessions(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		list, err := h.Accounts.Sessions(r.Context(), actor, bearer(r))
		respond(w, r, http.StatusOK, list, err, "")
	}
}

// revokeSession logs one of the caller's devices out.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Accounts.RevokeSession(r.Context(), actor, r.PathValue("id")), "session not found")
	}
}

// banUser bans or suspends a user.
//
// Without until the ban is permanent; with until (RFC 3339, future) it is a
// suspension lifted automatically. The user's sessions end, they cannot log
// in, and they are emailed the reason. Banning again replaces the reason
// and end date. Admins can only be banned by a super admin.
// Auth: user.ban (moderator, admin).
//
// spector:tags admin
func (h *Handler) banUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in sanctionInput
	if ok && decode(w, r, &in) {
		s, err := h.Accounts.Sanction(r.Context(), actor, r.PathValue("username"), in.Reason, in.Until)
		respond(w, r, http.StatusOK, s, err, "user not found")
	}
}

// getBan returns a user's current ban or suspension (404 when none).
//
// Auth: user.ban.
//
// spector:tags admin
func (h *Handler) getBan(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		s, err := h.Accounts.GetSanction(r.Context(), r.PathValue("username"))
		respond(w, r, http.StatusOK, s, err, "no ban")
	}
}

// unbanUser lifts a ban or suspension.
//
// Auth: user.ban.
//
// spector:tags admin
func (h *Handler) unbanUser(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Accounts.LiftSanction(r.Context(), actor, r.PathValue("username")), "no ban")
	}
}

// listBans lists banned and suspended users, newest first.
//
// Auth: user.ban.
//
// spector:tags admin
func (h *Handler) listBans(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		page, err := h.Accounts.ListSanctions(r.Context(), paging(r))
		respond(w, r, http.StatusOK, page, err, "")
	}
}

// block blocks a user.
//
// Neither side can follow the other (existing follows are removed); the
// blocked user cannot comment on your posts or reply to your comments, and
// you get no notifications from them.
// Auth: user.
//
// spector:tags users
func (h *Handler) block(w http.ResponseWriter, r *http.Request) { h.relate(w, r, relation.Block) }

// unblock removes a block.
//
// Auth: user.
//
// spector:tags users
func (h *Handler) unblock(w http.ResponseWriter, r *http.Request) { h.unrelate(w, r, relation.Block) }

// mute silences notifications from a user.
//
// Auth: user.
//
// spector:tags users
func (h *Handler) mute(w http.ResponseWriter, r *http.Request) { h.relate(w, r, relation.Mute) }

// unmute removes a mute.
//
// Auth: user.
//
// spector:tags users
func (h *Handler) unmute(w http.ResponseWriter, r *http.Request) { h.unrelate(w, r, relation.Mute) }

// myBlocks lists users the caller blocked, newest first.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) myBlocks(w http.ResponseWriter, r *http.Request) { h.related(w, r, relation.Block) }

// myMutes lists users the caller muted, newest first.
//
// Auth: user.
//
// spector:tags me
func (h *Handler) myMutes(w http.ResponseWriter, r *http.Request) { h.related(w, r, relation.Mute) }

func (h *Handler) relate(w http.ResponseWriter, r *http.Request, k relation.Kind) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.Relate(r.Context(), actor, r.PathValue("username"), k), "user not found")
	}
}

func (h *Handler) unrelate(w http.ResponseWriter, r *http.Request, k relation.Kind) {
	if actor, ok := h.requireUser(w, r); ok {
		noContent(w, r, h.Blog.Unrelate(r.Context(), actor, r.PathValue("username"), k), "user not found")
	}
}

func (h *Handler) related(w http.ResponseWriter, r *http.Request, k relation.Kind) {
	if actor, ok := h.requireUser(w, r); ok {
		page, err := h.Blog.Related(r.Context(), actor, k, paging(r))
		respond(w, r, http.StatusOK, page, err, "")
	}
}
