package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/mfa"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/sanction"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

const mfaChallengeTTL = 5 * time.Minute

// Two-factor authentication

func (a *Accounts) mfaEnabled(ctx context.Context, userID int) (bool, error) {
	if a.MFA == nil {
		return false, nil
	}
	s, err := a.MFA.Get(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return s.Enabled(), err
}

// mfaChallenge checks the password (Guard counts failures and locks) and
// returns a challenge instead of a session.
func (a *Accounts) mfaChallenge(ctx context.Context, u user.User, password string) (Session, error) {
	if err := a.identity.VerifyPassword(ctx, u.Email, password); err != nil {
		return Session{}, err
	}
	token, err := a.Challenges.Issue(ctx, u.ID, mfaChallengeTTL)
	if err != nil {
		return Session{}, err
	}
	return Session{MFARequired: true, MFAToken: token}, nil
}

// LoginMFA finishes a two-factor login with a TOTP or backup code. A
// challenge allows a few guesses within five minutes.
func (a *Accounts) LoginMFA(ctx context.Context, token, code string, m Meta) (Session, error) {
	ctx, span := tracer.Start(ctx, "Accounts.LoginMFA")
	defer span.End()
	if a.Challenges == nil {
		return Session{}, ErrInvalidCredentials
	}
	id, err := a.Challenges.Attempt(ctx, token)
	if err != nil {
		return Session{}, domain.Invalid("invalid or expired mfa_token")
	}
	if err := a.checkSecondFactor(ctx, id, code); err != nil {
		a.blog.Audit.Record(ctx, id, "auth.mfa_failed", "user:"+itoa(id), nil)
		return Session{}, err
	}
	if err := a.Challenges.Consume(ctx, token); err != nil {
		return Session{}, domain.Invalid("invalid or expired mfa_token")
	}
	u, err := a.users.GetByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	c, err := a.identity.StartSession(ctx, id, m)
	if err != nil {
		return Session{}, err
	}
	if u.Roles, err = a.identity.Roles(ctx, id); err != nil {
		return Session{}, err
	}
	a.blog.Audit.Record(ctx, id, "auth.login_mfa", "user:"+itoa(id), map[string]any{"ip": m.IP})
	return a.finishLogin(ctx, u, Session{Token: c.Token, ExpiresAt: c.ExpiresAt, User: u})
}

// checkSecondFactor accepts a current TOTP code (each only once) or an
// unused backup code.
func (a *Accounts) checkSecondFactor(ctx context.Context, userID int, code string) error {
	s, err := a.MFA.Get(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !s.Enabled()) {
		return mfa.ErrNotEnabled
	}
	if err != nil {
		return err
	}
	if mfa.IsBackupCode(code) {
		ok, err := a.MFA.UseBackupCode(ctx, userID, mfa.HashBackupCode(code))
		if err == nil && !ok {
			err = mfa.ErrBadCode
		}
		if err == nil {
			a.blog.Audit.Record(ctx, userID, "account.mfa_backup_code_used", "user:"+itoa(userID), nil)
		}
		return err
	}
	secret, err := a.Secrets.Open(s.Secret)
	if err != nil {
		return err
	}
	step, ok := mfa.Verify(secret, code, a.now())
	if !ok {
		return mfa.ErrBadCode
	}
	fresh, err := a.MFA.UseStep(ctx, userID, step)
	if err == nil && !fresh {
		err = mfa.ErrBadCode // replayed
	}
	return err
}

func (a *Accounts) MFAStatus(ctx context.Context, actor user.User) (mfa.TwoFactorStatus, error) {
	ctx, span := tracer.Start(ctx, "Accounts.MFAStatus")
	defer span.End()
	s, err := a.MFA.Get(ctx, actor.ID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !s.Enabled()) {
		return mfa.TwoFactorStatus{}, nil
	}
	if err != nil {
		return mfa.TwoFactorStatus{}, err
	}
	left, err := a.MFA.BackupCodesLeft(ctx, actor.ID)
	return mfa.TwoFactorStatus{Enabled: true, EnabledAt: s.EnabledAt, BackupCodes: left}, err
}

// SetupMFA starts (or restarts) setup with a new secret; it is off until
// EnableMFA confirms a code from the app.
func (a *Accounts) SetupMFA(ctx context.Context, actor user.User) (mfa.Setup, error) {
	ctx, span := tracer.Start(ctx, "Accounts.SetupMFA")
	defer span.End()
	secret := mfa.NewSecret()
	sealed, err := a.Secrets.Seal(secret)
	if err != nil {
		return mfa.Setup{}, err
	}
	if err := a.MFA.SavePending(ctx, actor.ID, sealed); err != nil {
		return mfa.Setup{}, err
	}
	account := actor.Email
	if account == "" {
		account = actor.Username
	}
	return mfa.Setup{Secret: secret, URI: mfa.URI(secret, account)}, nil
}

// EnableMFA confirms setup with a code and returns backup codes (shown once).
func (a *Accounts) EnableMFA(ctx context.Context, actor user.User, code string) ([]string, error) {
	ctx, span := tracer.Start(ctx, "Accounts.EnableMFA")
	defer span.End()
	s, err := a.MFA.Get(ctx, actor.ID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, mfa.ErrNoSetup
	case err != nil:
		return nil, err
	case s.Enabled():
		return nil, mfa.ErrEnabled
	}
	secret, err := a.Secrets.Open(s.Secret)
	if err != nil {
		return nil, err
	}
	step, ok := mfa.Verify(secret, code, a.now())
	if !ok {
		return nil, mfa.ErrBadCode
	}
	codes := mfa.NewBackupCodes()
	if err := a.MFA.Enable(ctx, actor.ID, step, hashCodes(codes)); err != nil {
		return nil, err
	}
	a.blog.Audit.Record(ctx, actor.ID, "account.mfa_enable", "user:"+itoa(actor.ID), nil)
	a.securityEmail(ctx, actor, "Two-factor authentication enabled",
		"Two-factor authentication is now on for your account.")
	return codes, nil
}

// DisableMFA needs the password and a current code (or backup code).
func (a *Accounts) DisableMFA(ctx context.Context, actor user.User, password, code string) error {
	ctx, span := tracer.Start(ctx, "Accounts.DisableMFA")
	defer span.End()
	if err := a.identity.VerifyPassword(ctx, actor.Email, password); err != nil {
		return err
	}
	if err := a.checkSecondFactor(ctx, actor.ID, code); err != nil {
		return err
	}
	if err := a.MFA.Disable(ctx, actor.ID); err != nil {
		return err
	}
	a.blog.Audit.Record(ctx, actor.ID, "account.mfa_disable", "user:"+itoa(actor.ID), nil)
	a.securityEmail(ctx, actor, "Two-factor authentication disabled",
		"Two-factor authentication was turned off for your account. If this was not you, reset your password now.")
	return nil
}

// RegenerateBackupCodes replaces every backup code; it needs a current code.
func (a *Accounts) RegenerateBackupCodes(ctx context.Context, actor user.User, code string) ([]string, error) {
	ctx, span := tracer.Start(ctx, "Accounts.RegenerateBackupCodes")
	defer span.End()
	if err := a.checkSecondFactor(ctx, actor.ID, code); err != nil {
		return nil, err
	}
	codes := mfa.NewBackupCodes()
	if err := a.MFA.ReplaceBackupCodes(ctx, actor.ID, hashCodes(codes)); err != nil {
		return nil, err
	}
	a.blog.Audit.Record(ctx, actor.ID, "account.mfa_backup_codes", "user:"+itoa(actor.ID), nil)
	return codes, nil
}

func hashCodes(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = mfa.HashBackupCode(c)
	}
	return out
}

func (a *Accounts) securityEmail(ctx context.Context, u user.User, subject, text string) {
	if u.Email == "" {
		return
	}
	err := a.blog.Jobs.SendEmail(ctx, Email{To: u.Email, Subject: subject,
		Text: fmt.Sprintf("Hi %s,\n\n%s\n", u.Username, text)})
	if err != nil {
		zap.L().Error("security email", zap.Any("ctx", ctx), zap.Int("user", u.ID), zap.Error(err))
	}
}

// Sessions

// Sessions lists the caller's logged-in devices, most recently used first.
func (a *Accounts) Sessions(ctx context.Context, actor user.User, token string) ([]SessionInfo, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Sessions")
	defer span.End()
	list, err := a.identity.Sessions(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	cur, _ := a.identity.SessionID(ctx, token)
	for i := range list {
		list[i].Current = list[i].ID == cur
	}
	slices.SortFunc(list, func(x, y SessionInfo) int { return y.LastSeenAt.Compare(x.LastSeenAt) })
	if list == nil {
		list = []SessionInfo{}
	}
	return list, nil
}

func (a *Accounts) RevokeSession(ctx context.Context, actor user.User, id string) error {
	ctx, span := tracer.Start(ctx, "Accounts.RevokeSession")
	defer span.End()
	if err := a.identity.RevokeSession(ctx, actor.ID, id); err != nil {
		return err
	}
	a.blog.Audit.Record(ctx, actor.ID, "auth.session_revoke", "user:"+itoa(actor.ID), nil)
	return nil
}

// Bans and suspensions (user.ban)

// Sanction bans (no until) or suspends (until) a user: their sessions end,
// they cannot log in, and they get an email with the reason.
func (a *Accounts) Sanction(ctx context.Context, actor user.User, username, reason string, until *time.Time) (sanction.Sanction, error) {
	ctx, span := tracer.Start(ctx, "Accounts.Sanction")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "user", "ban", 0, 0); err != nil {
		return sanction.Sanction{}, err
	}
	target, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		return sanction.Sanction{}, err
	}
	s, err := sanction.New(target.ID, actor.ID, reason, until, a.now())
	if err != nil {
		return s, err
	}
	if err := a.identity.SetBlocked(ctx, actor.ID, target.ID, true); err != nil {
		return sanction.Sanction{}, err
	}
	if s, err = a.Sanctions.Save(ctx, s); err != nil {
		return s, err
	}
	a.blog.Audit.Record(ctx, actor.ID, "user."+string(s.Kind), "user:"+itoa(target.ID),
		map[string]any{"reason": s.Reason, "until": s.Until})
	text := "Your account has been banned.\n\nReason: " + s.Reason
	if s.Until != nil {
		text = fmt.Sprintf("Your account is suspended until %s.\n\nReason: %s", s.Until.UTC().Format(time.RFC1123), s.Reason)
	}
	a.securityEmail(ctx, target, "Your account has been "+string(s.Kind), text)
	return s, nil
}

func (a *Accounts) GetSanction(ctx context.Context, username string) (sanction.Sanction, error) {
	ctx, span := tracer.Start(ctx, "Accounts.GetSanction")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "user", "ban", 0, 0); err != nil {
		return sanction.Sanction{}, err
	}
	target, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		return sanction.Sanction{}, err
	}
	return a.Sanctions.Get(ctx, target.ID)
}

func (a *Accounts) ListSanctions(ctx context.Context, p domain.Paging) (sanction.Page, error) {
	ctx, span := tracer.Start(ctx, "Accounts.ListSanctions")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "user", "ban", 0, 0); err != nil {
		return sanction.Page{}, err
	}
	items, total, err := a.Sanctions.List(ctx, p)
	return sanction.Page{Items: items, Total: total, Page: p.Page, Limit: p.Limit}, err
}

// LiftSanction reactivates a banned or suspended user.
func (a *Accounts) LiftSanction(ctx context.Context, actor user.User, username string) error {
	ctx, span := tracer.Start(ctx, "Accounts.LiftSanction")
	defer span.End()
	if err := a.blog.Authz.Can(ctx, "user", "ban", 0, 0); err != nil {
		return err
	}
	target, err := a.users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	if _, err := a.Sanctions.Get(ctx, target.ID); err != nil {
		return err
	}
	return a.lift(ctx, actor.ID, target.ID)
}

func (a *Accounts) lift(ctx context.Context, actorID, userID int) error {
	if err := a.identity.SetBlocked(ctx, actorID, userID, false); err != nil {
		return err
	}
	if err := a.Sanctions.Delete(ctx, userID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	a.blog.Audit.Record(ctx, actorID, "user.unban", "user:"+itoa(userID), nil)
	return nil
}

// LiftExpired ends suspensions whose time is up (periodic job).
func (a *Accounts) LiftExpired(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Accounts.LiftExpired")
	defer span.End()
	if a.Sanctions == nil {
		return nil
	}
	ids, err := a.Sanctions.Expired(ctx, a.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		errs = append(errs, a.lift(ctx, 0, id))
	}
	return errors.Join(errs...)
}
