package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/mfa"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/sanction"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

var ErrInvalidCredentials = errors.New("invalid login or password")

const (
	resetTokenTTL  = time.Hour
	verifyTokenTTL = 48 * time.Hour
)

// Accounts handles sign-up, sessions, passwords, profiles, follows and the
// account deletion lifecycle.
type Accounts struct {
	users    user.Repository
	identity Identity
	resets   ResetTokens
	verifies ResetTokens
	blog     *Blog
	siteURL  string
	now      func() time.Time

	// Two-factor authentication, sanctions and blocks (optional; see security.go).
	MFA        mfa.Repository
	Challenges MFAChallenges
	Secrets    SecretBox
	Sanctions  sanction.Repository
}

// NewAccounts wires accounts; resets and verifies are separate token stores
// (password reset and email verification).
func NewAccounts(users user.Repository, id Identity, resets, verifies ResetTokens, blog *Blog, siteURL string) *Accounts {
	return &Accounts{users: users, identity: id, resets: resets, verifies: verifies, blog: blog, siteURL: siteURL, now: time.Now}
}

// Register creates the user and its Guard account (default role "user").
func (a *Accounts) Register(ctx context.Context, username, email, password string, m Meta) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Register")
	defer span.End()
	reg, err := user.NewRegistration(username, email, password)
	if err != nil {
		return Session{}, err
	}
	u, err := a.users.Create(ctx, reg.Username, reg.Email)
	if err != nil {
		return Session{}, err
	}
	if err := a.identity.CreateAccount(ctx, u.ID, reg.Email, reg.Password); err != nil {
		if derr := a.users.Delete(ctx, u.ID); derr != nil {
			zap.L().Error("rollback user", zap.Any("ctx", ctx), zap.Int("id", u.ID), zap.Error(derr))
		}
		return Session{}, err
	}
	if err := a.sendVerification(ctx, u); err != nil {
		zap.L().Error("verification email", zap.Any("ctx", ctx), zap.Int("user", u.ID), zap.Error(err)) // resend is possible
	}
	return a.startSession(ctx, u, reg.Password, m)
}

// ResendVerification emails a new verification link.
func (a *Accounts) ResendVerification(ctx context.Context, actor user.User) error {
	ctx, span := tracer.Start(ctx, "Accounts.ResendVerification")
	defer span.End()
	if actor.EmailVerified {
		return domain.Invalid("email already verified")
	}
	return a.sendVerification(ctx, actor)
}

func (a *Accounts) sendVerification(ctx context.Context, u user.User) error {
	token, err := a.verifies.Issue(ctx, u.ID, verifyTokenTTL)
	if err != nil {
		return err
	}
	return a.blog.Jobs.SendEmail(ctx, Email{
		To:      u.Email,
		Subject: "Confirm your email",
		Text: fmt.Sprintf("Hi %s,\n\nConfirm your email within 48 hours:\n%s/verify-email?code=%s\n\nIgnore this email if you did not sign up.\n",
			u.Username, a.siteURL, token),
	})
}

// VerifyEmail marks the email of the token's user as verified.
func (a *Accounts) VerifyEmail(ctx context.Context, token string) error {
	ctx, span := tracer.Start(ctx, "Accounts.VerifyEmail")
	defer span.End()
	id, err := a.verifies.Consume(ctx, token)
	if err != nil {
		return domain.Invalid("invalid or expired code")
	}
	if err := a.users.MarkEmailVerified(ctx, id); err != nil {
		return err
	}
	a.blog.Audit.Record(ctx, id, "account.email_verified", "user:"+itoa(id), nil)
	return nil
}

// timingEmail never belongs to an account (.invalid is reserved, RFC 2606);
// verifying against it runs Guard's dummy hash for unknown logins.
const timingEmail = "no-account@login-timing.invalid"

// Login accepts a username or email. Logging in to an account with a
// pending deletion restores it.
func (a *Accounts) Login(ctx context.Context, login, password string, m Meta) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Login")
	defer span.End()
	u, err := a.users.GetByLogin(ctx, login)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && u.Email == "") {
		// Pay the same password-hash cost as a real attempt so response
		// time does not reveal whether the login exists.
		_ = a.identity.VerifyPassword(ctx, timingEmail, password)
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if on, err := a.mfaEnabled(ctx, u.ID); err != nil || on {
		if err != nil {
			return Session{}, err
		}
		return a.mfaChallenge(ctx, u, password)
	}
	s, err := a.startSession(ctx, u, password, m)
	if err != nil {
		return s, err
	}
	return a.finishLogin(ctx, u, s)
}

// finishLogin restores an account with a pending deletion.
func (a *Accounts) finishLogin(ctx context.Context, u user.User, s Session) (Session, error) {
	if u.DeletedAt != nil {
		if err := a.users.Restore(ctx, u.ID); err != nil {
			return Session{}, err
		}
		s.User.DeletedAt = nil
		a.blog.Audit.Record(ctx, u.ID, "account.restore", "user:"+itoa(u.ID), nil)
	}
	return s, nil
}

func (a *Accounts) startSession(ctx context.Context, u user.User, password string, m Meta) (Session, error) {
	c, err := a.identity.Login(ctx, u.Email, password, m)
	if err != nil {
		return Session{}, err
	}
	if u.Roles, err = a.identity.Roles(ctx, u.ID); err != nil {
		return Session{}, err
	}
	return Session{Token: c.Token, ExpiresAt: c.ExpiresAt, User: u}, nil
}

func (a *Accounts) Refresh(ctx context.Context, token string) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Refresh")
	defer span.End()
	id, err := a.identity.Authenticate(ctx, token)
	if err != nil {
		return Session{}, err
	}
	c, err := a.identity.Refresh(ctx, token)
	if err != nil {
		return Session{}, err
	}
	u, err := a.User(ctx, id)
	return Session{Token: c.Token, ExpiresAt: c.ExpiresAt, User: u}, err
}

func (a *Accounts) Logout(ctx context.Context, token string, m Meta) error {
	ctx, span := tracer.Start(ctx, "Accounts.Logout")
	defer span.End()
	return a.identity.Logout(ctx, token, m)
}

func (a *Accounts) LogoutAll(ctx context.Context, actor user.User) error {
	ctx, span := tracer.Start(ctx, "Accounts.LogoutAll")
	defer span.End()
	return a.identity.LogoutAll(ctx, actor.ID)
}

// User loads a user with their roles; deleted-pending users are refused.
func (a *Accounts) User(ctx context.Context, id int) (user.User, error) {
	ctx, span := tracer.Start(ctx, "Accounts.User")
	defer span.End()
	u, err := a.users.GetByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return u, domain.ErrUnauthorized
	}
	if err != nil {
		return u, err
	}
	if u.DeletedAt != nil {
		return u, domain.ErrUnauthorized
	}
	u.Roles, err = a.identity.Roles(ctx, id)
	return u, err
}

// Authenticate resolves a bearer token to its user.
func (a *Accounts) Authenticate(ctx context.Context, token string) (user.User, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Authenticate")
	defer span.End()
	id, err := a.identity.Authenticate(ctx, token)
	if err != nil {
		return user.User{}, domain.ErrUnauthorized
	}
	return a.User(ctx, id)
}

// ChangePassword ends every session, revokes every API key and returns a fresh session.
func (a *Accounts) ChangePassword(ctx context.Context, actor user.User, oldPassword, newPassword string, m Meta) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.ChangePassword")
	defer span.End()
	if err := user.ValidatePassword(newPassword); err != nil {
		return Session{}, err
	}
	if err := a.identity.ChangePassword(ctx, actor.ID, oldPassword, newPassword); err != nil {
		return Session{}, err
	}
	if err := a.identity.LogoutAll(ctx, actor.ID); err != nil {
		return Session{}, err
	}
	a.blog.Audit.Record(ctx, actor.ID, "account.password_change", "user:"+itoa(actor.ID), nil)
	return a.startSession(ctx, actor, newPassword, m)
}

// ForgotPassword emails a reset link. It never reveals whether the address exists.
func (a *Accounts) ForgotPassword(ctx context.Context, email string) error {
	ctx, span := tracer.Start(ctx, "Accounts.ForgotPassword")
	defer span.End()
	addr, err := user.ValidateEmail(email)
	if err != nil {
		return err
	}
	u, err := a.users.GetByLogin(ctx, addr)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && u.DeletedAt != nil) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := a.resets.Issue(ctx, u.ID, resetTokenTTL)
	if err != nil {
		return err
	}
	return a.blog.Jobs.SendEmail(ctx, Email{
		To:      u.Email,
		Subject: "Reset your password",
		Text: fmt.Sprintf("Hi %s,\n\nReset your password within an hour:\n%s/reset-password?token=%s\n\nIgnore this email if you did not ask.\n",
			u.Username, a.siteURL, token),
	})
}

// ResetPassword sets a new password from an emailed token, ends every session and revokes every API key.
func (a *Accounts) ResetPassword(ctx context.Context, token, password string) error {
	ctx, span := tracer.Start(ctx, "Accounts.ResetPassword")
	defer span.End()
	if err := user.ValidatePassword(password); err != nil {
		return err
	}
	id, err := a.resets.Consume(ctx, token)
	if err != nil {
		return domain.Invalid("invalid or expired token")
	}
	if err := a.identity.SetPassword(ctx, id, password); err != nil {
		return err
	}
	a.blog.Audit.Record(ctx, id, "account.password_reset", "user:"+itoa(id), nil)
	return nil
}

// DeleteAccount starts the 30-day deletion grace period: sessions end now,
// the account is purged later unless its owner logs in again.
func (a *Accounts) DeleteAccount(ctx context.Context, actor user.User, password string) (time.Time, error) {
	ctx, span := tracer.Start(ctx, "Accounts.DeleteAccount")
	defer span.End()
	if err := a.identity.VerifyPassword(ctx, actor.Email, password); err != nil {
		return time.Time{}, ErrInvalidCredentials
	}
	now := a.now()
	if err := a.users.MarkDeleted(ctx, actor.ID, now); err != nil {
		return time.Time{}, err
	}
	if err := a.identity.LogoutAll(ctx, actor.ID); err != nil {
		return time.Time{}, err
	}
	purgeAt := now.Add(user.DeletionGrace)
	if err := a.blog.Jobs.PurgeAccountAt(ctx, actor.ID, purgeAt); err != nil {
		zap.L().Error("schedule purge", zap.Any("ctx", ctx), zap.Int("user", actor.ID), zap.Error(err))
	}
	a.blog.Audit.Record(ctx, actor.ID, "account.delete_request", "user:"+itoa(actor.ID), map[string]any{"purge_at": purgeAt})
	return purgeAt, nil
}

// Purge removes one account whose grace period has ended (job handler).
func (a *Accounts) Purge(ctx context.Context, id int) error {
	ctx, span := tracer.Start(ctx, "Accounts.Purge")
	defer span.End()
	ok, err := a.users.Purge(ctx, id, a.now().Add(-user.DeletionGrace))
	if err == nil && ok {
		a.blog.Audit.Record(ctx, 0, "account.purge", "user:"+itoa(id), nil)
	}
	return err
}

// PurgeDue removes every account past its grace period (periodic safety net).
func (a *Accounts) PurgeDue(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Accounts.PurgeDue")
	defer span.End()
	ids, err := a.users.DuePurges(ctx, a.now().Add(-user.DeletionGrace))
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		errs = append(errs, a.Purge(ctx, id))
	}
	return errors.Join(errs...)
}

// EnsureAdmin seeds the super admin at startup.
func (a *Accounts) EnsureAdmin(ctx context.Context, username, email, password string) error {
	ctx, span := tracer.Start(ctx, "Accounts.EnsureAdmin")
	defer span.End()
	reg, err := user.NewRegistration(username, email, password)
	if err != nil {
		return err
	}
	u, err := a.users.Ensure(ctx, reg.Username, reg.Email)
	if err != nil {
		return err
	}
	if err := a.users.MarkEmailVerified(ctx, u.ID); err != nil {
		return err
	}
	return a.identity.EnsureSuperAdmin(ctx, u.ID, reg.Email, reg.Password)
}

// Profiles and follows

func (a *Accounts) Profile(ctx context.Context, username string, viewerID int) (user.Profile, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Profile")
	defer span.End()
	return a.users.Profile(ctx, username, viewerID)
}

func (a *Accounts) UpdateProfile(ctx context.Context, actor user.User, u user.ProfileUpdate) (user.User, error) {
	ctx, span := tracer.Start(ctx, "Accounts.UpdateProfile")
	defer span.End()
	u, err := u.Normalize()
	if err != nil {
		return user.User{}, err
	}
	out, err := a.users.UpdateProfile(ctx, actor.ID, u)
	out.Roles = actor.Roles
	return out, err
}

func (a *Accounts) UserPosts(ctx context.Context, username string, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Accounts.UserPosts")
	defer span.End()
	u, err := a.users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		return post.Page{}, notFoundOr(err)
	}
	return a.blog.Posts.List(ctx, post.Filter{UserID: u.ID, Status: post.StatusPublished, Paging: p})
}

func (a *Accounts) Follow(ctx context.Context, actor user.User, username string) error {
	ctx, span := tracer.Start(ctx, "Accounts.Follow")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "follow", "create", 0, 0); err != nil {
		return err
	}
	target, err := a.users.GetByUsername(ctx, username)
	if err != nil || target.DeletedAt != nil {
		return notFoundOr(err)
	}
	if target.ID == actor.ID {
		return domain.Invalid("cannot follow yourself")
	}
	if err := a.blog.notBlocked(ctx, actor.ID, target.ID); err != nil {
		return err
	}
	created, err := a.users.Follow(ctx, actor.ID, target.ID)
	if err == nil && created {
		a.blog.notify(ctx, social.Notification{Type: social.NotifyFollow, ActorID: actor.ID}, target.ID)
	}
	return err
}

func (a *Accounts) Unfollow(ctx context.Context, actor user.User, username string) error {
	ctx, span := tracer.Start(ctx, "Accounts.Unfollow")
	defer span.End()
	target, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	return a.users.Unfollow(ctx, actor.ID, target.ID)
}

func (a *Accounts) Followers(ctx context.Context, username string, p domain.Paging) (user.SummaryPage, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Followers")
	defer span.End()
	u, err := a.users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		return user.SummaryPage{}, notFoundOr(err)
	}
	return a.users.Followers(ctx, u.ID, p)
}

func (a *Accounts) Following(ctx context.Context, username string, p domain.Paging) (user.SummaryPage, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Following")
	defer span.End()
	u, err := a.users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		return user.SummaryPage{}, notFoundOr(err)
	}
	return a.users.Following(ctx, u.ID, p)
}

// ListUsers is the admin view (user.read).
func (a *Accounts) ListUsers(ctx context.Context, query string, p domain.Paging) (user.Page, error) {
	ctx, span := tracer.Start(ctx, "Accounts.ListUsers")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "user", "read", 0, 0); err != nil {
		return user.Page{}, err
	}
	page, err := a.users.List(ctx, query, p)
	if err != nil {
		return user.Page{}, err
	}
	// Roles live in Guard, not the users table.
	for i := range page.Items {
		if page.Items[i].Roles, err = a.identity.Roles(ctx, page.Items[i].ID); err != nil {
			return user.Page{}, err
		}
	}
	return page, nil
}

func notFoundOr(err error) error {
	if err == nil {
		return domain.ErrNotFound
	}
	return err
}
