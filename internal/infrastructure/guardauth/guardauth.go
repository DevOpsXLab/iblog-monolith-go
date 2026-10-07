// Package guardauth adapts github.com/bakhod1r/guard to the application
// ports: Identity (accounts and sessions), Authorizer (RBAC + ABAC) and
// Auditor. It also seeds the blog's roles, permissions and policies.
package guardauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bakhod1r/guard"
	accessdomain "github.com/bakhod1r/guard/access/domain"
	"github.com/bakhod1r/guard/audit"
	"github.com/bakhod1r/guard/httpguard"
	identitydomain "github.com/bakhod1r/guard/identity/domain"
	sessionapp "github.com/bakhod1r/guard/session/application"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

// Open creates Guard over the blog's users table. With o.Migrate it also
// applies Guard's migrations and seeds the blog's access rules under the
// setup advisory lock (see setup). Without it Open runs no DDL and no seed:
// cmd/migrate (Migrate) owns both, so replicas can use a DML-only DB user.
func Open(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, o Options) (*guard.Guard, error) {
	hash, err := o.hashParams()
	if err != nil {
		return nil, err
	}
	emailKey, err := o.auditEmailKey()
	if err != nil {
		return nil, err
	}
	if emailKey == nil {
		zap.L().Warn("AUDIT_EMAIL_KEY not set: audit events store an unkeyed email fingerprint", zap.Any("ctx", ctx))
	}
	g, err := guard.New(guard.Config{
		DB:                 db,
		Redis:              rdb,
		UserTable:          "users",
		AccessCache:        &guard.AccessCacheOptions{},
		Logger:             slog.New(zapslog.NewHandler(zap.L().Core())),
		PasswordHashParams: hash,
		AuditEmailKey:      emailKey,
	})
	if err != nil {
		return nil, err
	}
	if o.Migrate {
		if err := setup(ctx, db, g, o.AccessRules); err != nil {
			_ = g.Close(context.Background())
			return nil, err
		}
	}
	return g, nil
}

func uid(id int) string { return strconv.Itoa(id) }

// Principal context

type principalKey struct{}

// WithPrincipal copies the principal httpguard.Authenticate attached to the
// request into its context, so the application layer can authorize without
// knowing about HTTP.
func WithPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := httpguard.PrincipalFrom(r); p != nil {
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		}
		next.ServeHTTP(w, r)
	})
}

func principal(ctx context.Context) *guard.Principal {
	p, _ := ctx.Value(principalKey{}).(*guard.Principal)
	return p
}

// UserID returns the authenticated caller's id (0 when anonymous).
func UserID(ctx context.Context) int {
	if p := principal(ctx); p != nil {
		id, _ := strconv.Atoi(string(p.User.ID))
		return id
	}
	return 0
}

// Authorizer is the RBAC + ABAC decision point.
type Authorizer struct{ G *guard.Guard }

// Can asks Guard: ABAC policies first (by priority, deny wins), then the
// caller's role permissions. The resource carries owner_id for ownership
// policies.
func (a Authorizer) Can(ctx context.Context, resource, action string, id, ownerID int) error {
	p := principal(ctx)
	if p == nil {
		return domain.ErrUnauthorized
	}
	res := guard.Resource{Type: resource, Attributes: map[string]any{"owner_id": uid(ownerID)}}
	if id != 0 {
		res.ID = uid(id)
	}
	d, err := a.G.Authorize(ctx, p, action, res, environment(ctx))
	if err != nil {
		return err
	}
	if !d.Allowed {
		return domain.ErrForbidden
	}
	return nil
}

// Auditor writes to Guard's audit log, so blog events and security events
// share one trail (GET /api/guard/audit).
type Auditor struct{ G *guard.Guard }

func (a Auditor) Record(ctx context.Context, actorID int, action, target string, meta map[string]any) {
	e := audit.Event{Action: action, Target: target, Success: true, Metadata: meta}
	if actorID != 0 {
		e.ActorID = uid(actorID)
	}
	if err := a.G.Audit.Record(ctx, e); err != nil {
		zap.L().Warn("audit", zap.Any("ctx", ctx), zap.Any("action", action), zap.Error(err))
	}
}

// Identity implements application.Identity with Guard accounts and sessions.
type Identity struct{ G *guard.Guard }

func (i Identity) CreateAccount(ctx context.Context, userID int, email, password string) error {
	_, err := i.G.CreateAccount(ctx, uid(userID), email, password, nil, guard.RequestMeta{})
	if errors.Is(err, identitydomain.ErrEmailTaken) || errors.Is(err, identitydomain.ErrAccountExists) {
		return domain.ErrConflict
	}
	return err
}

func (i Identity) EnsureSuperAdmin(ctx context.Context, userID int, email, password string) error {
	_, err := i.G.EnsureSuperAdmin(ctx, uid(userID), email, password)
	return err
}

func credentialsErr(err error) error {
	switch {
	case errors.Is(err, identitydomain.ErrInvalidCredentials), errors.Is(err, identitydomain.ErrUserBlocked),
		errors.Is(err, identitydomain.ErrUserNotFound), errors.Is(err, identitydomain.ErrInvalidEmail):
		return application.ErrInvalidCredentials
	case errors.Is(err, identitydomain.ErrUserLocked):
		return domain.Invalid("too many failed attempts, try later")
	}
	return err
}

func (i Identity) Login(ctx context.Context, email, password string, m application.Meta) (application.Credentials, error) {
	res, err := i.G.Login(ctx, email, password, guard.RequestMeta{IP: m.IP, UserAgent: m.UserAgent})
	if err != nil {
		return application.Credentials{}, credentialsErr(err)
	}
	return application.Credentials{Token: string(res.Token), ExpiresAt: res.Session.ExpiresAt}, nil
}

func (i Identity) Refresh(ctx context.Context, token string) (application.Credentials, error) {
	s, tok, err := i.G.RotateSession(ctx, guard.SessionToken(token))
	if err != nil {
		return application.Credentials{}, domain.ErrUnauthorized
	}
	return application.Credentials{Token: string(tok), ExpiresAt: s.ExpiresAt}, nil
}

func (i Identity) Logout(ctx context.Context, token string, m application.Meta) error {
	p, err := i.G.Authenticate(ctx, token)
	if err != nil {
		return domain.ErrUnauthorized
	}
	return i.G.Logout(ctx, p, guard.RequestMeta{IP: m.IP, UserAgent: m.UserAgent})
}

// LogoutAll ends every session and revokes every API key of the user, so a
// credential minted by an intruder does not survive a password change,
// reset, sign-out-everywhere or account deletion.
func (i Identity) LogoutAll(ctx context.Context, userID int) error {
	return i.revokeCredentials(ctx, userID)
}

func (i Identity) revokeCredentials(ctx context.Context, userID int) error {
	if err := i.G.Sessions.RevokeAll(ctx, uid(userID)); err != nil {
		return err
	}
	return i.G.APIKeys.RevokeAll(ctx, uid(userID))
}

func (i Identity) Authenticate(ctx context.Context, token string) (int, error) {
	p, err := i.G.Authenticate(ctx, token)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}
	return strconv.Atoi(string(p.User.ID))
}

func (i Identity) VerifyPassword(ctx context.Context, email, password string) error {
	_, err := i.G.Identity.Authenticate(ctx, email, password)
	return credentialsErr(err)
}

func (i Identity) ChangePassword(ctx context.Context, userID int, oldPassword, newPassword string) error {
	err := i.G.Identity.ChangePassword(ctx, identitydomain.UserID(uid(userID)), oldPassword, newPassword)
	if errors.Is(err, identitydomain.ErrInvalidCredentials) {
		return domain.Invalid("current password is wrong")
	}
	return err
}

func (i Identity) SetPassword(ctx context.Context, userID int, password string) error {
	if err := i.G.Identity.SetPassword(ctx, identitydomain.UserID(uid(userID)), password); err != nil {
		return err
	}
	return i.revokeCredentials(ctx, userID)
}

func (i Identity) StartSession(ctx context.Context, userID int, m application.Meta) (application.Credentials, error) {
	u, err := i.G.Identity.User(ctx, identitydomain.UserID(uid(userID)))
	if err != nil {
		return application.Credentials{}, credentialsErr(err)
	}
	if err := u.CanLogin(); err != nil {
		return application.Credentials{}, credentialsErr(err)
	}
	s, tok, err := i.G.Sessions.Start(ctx, sessionapp.StartInput{UserID: uid(userID), IP: m.IP, UserAgent: m.UserAgent})
	if err != nil {
		return application.Credentials{}, err
	}
	return application.Credentials{Token: string(tok), ExpiresAt: s.ExpiresAt}, nil
}

func (i Identity) Sessions(ctx context.Context, userID int) ([]application.SessionInfo, error) {
	list, err := i.G.Sessions.List(ctx, uid(userID))
	if err != nil {
		return nil, err
	}
	out := make([]application.SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, application.SessionInfo{ID: string(s.ID), IP: s.IP, UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt})
	}
	return out, nil
}

func (i Identity) RevokeSession(ctx context.Context, userID int, sessionID string) error {
	list, err := i.G.Sessions.List(ctx, uid(userID))
	if err != nil {
		return err
	}
	for _, s := range list {
		if string(s.ID) == sessionID {
			return i.G.Sessions.Revoke(ctx, s.ID)
		}
	}
	return domain.ErrNotFound
}

func (i Identity) SessionID(ctx context.Context, token string) (string, error) {
	p, err := i.G.Authenticate(ctx, token)
	if err != nil || p.Session == nil {
		return "", domain.ErrUnauthorized
	}
	return string(p.Session.ID), nil
}

func (i Identity) SetBlocked(ctx context.Context, actorID, userID int, blocked bool) error {
	status := identitydomain.StatusActive
	if blocked {
		status = identitydomain.StatusBanned
	}
	var err error
	if actorID == 0 {
		err = i.G.Identity.SetStatus(ctx, identitydomain.UserID(uid(userID)), status)
	} else {
		err = i.G.SetUserStatus(ctx, uid(actorID), uid(userID), status)
	}
	switch {
	case errors.Is(err, identitydomain.ErrUserNotFound):
		return domain.ErrNotFound
	case errors.Is(err, accessdomain.ErrForbidden), errors.Is(err, accessdomain.ErrLastSuperAdmin):
		return domain.ErrForbidden
	}
	return err
}

func (i Identity) Roles(ctx context.Context, userID int) ([]string, error) {
	roles, err := i.G.Access.ActiveRoles(ctx, uid(userID))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	return names, nil
}

// RBAC + ABAC model

// rolePermissions are the RBAC grants. "admin" and "super_admin" are
// wildcard roles seeded by Guard.
var rolePermissions = map[string][]string{
	"user": {
		"app.access",
		"post.create", "comment.create", "like.create", "bookmark.create",
		"follow.create", "upload.create", "highlight.create", "report.create", "publication.create",
	},
	"moderator": {
		"app.access",
		"post.create", "comment.create", "like.create", "bookmark.create",
		"follow.create", "upload.create", "highlight.create", "highlight.delete",
		"report.create", "report.moderate", "publication.create", "user.ban",
		"post.read_draft", "post.update", "post.delete",
		"comment.update", "comment.delete", "comment.moderate",
		"user.read", "audit.read",
	},
}

var permissionDocs = map[string]string{
	"app.access":         "use the API at all; deny policies on it close access by time or network",
	"post.create":        "publish posts",
	"post.read_draft":    "read anyone's drafts and scheduled posts",
	"post.update":        "edit anyone's posts",
	"post.delete":        "delete anyone's posts",
	"comment.create":     "comment",
	"comment.update":     "edit anyone's comments",
	"comment.delete":     "delete anyone's comments",
	"comment.moderate":   "list all comments",
	"like.create":        "like posts",
	"bookmark.create":    "bookmark posts",
	"highlight.create":   "highlight passages",
	"highlight.delete":   "delete anyone's highlights",
	"report.create":      "report posts, comments and users",
	"report.moderate":    "list and decide reports",
	"publication.create": "create publications",
	"follow.create":      "follow users",
	"upload.create":      "upload images",
	"category.write":     "manage categories",
	"label.write":        "manage labels",
	"stats.read":         "read site stats",
	"user.read":          "list users",
	"user.ban":           "ban and suspend users",
	"audit.read":         "read the audit log",

	// Guard's own admin API (/api/guard/*); Guard checks these but does not
	// create them, so they are seeded here to be grantable.
	"user.write":       "create accounts, reset passwords, set user status and attributes",
	"role.read":        "list roles and users' roles",
	"role.write":       "create and delete roles, grant and revoke permissions",
	"role.assign":      "assign and unassign users' roles",
	"permission.read":  "list permissions",
	"permission.write": "create permissions",
	"policy.read":      "list ABAC policies",
	"policy.write":     "create, edit and delete ABAC policies",
	"session.read":     "list users' sessions",
	"session.revoke":   "revoke users' sessions",
	"apikey.read":      "list users' API keys",
	"apikey.revoke":    "revoke users' API keys",
}

// ownerPolicies are ABAC rules: the owner of a resource may act on it even
// without the RBAC permission. resource.owner_id is set by Authorizer.Can.
var ownerPolicies = []struct{ resource, action, name string }{
	{"post", "read_draft", "authors read own drafts"},
	{"post", "update", "authors edit own posts"},
	{"post", "delete", "authors delete own posts"},
	{"comment", "update", "authors edit own comments"},
	{"comment", "delete", "authors delete own comments"},
	{"highlight", "delete", "readers delete own highlights"},
}

// Seed creates the blog's permissions, the moderator role, role grants and
// ownership policies. It is idempotent.
func Seed(ctx context.Context, g *guard.Guard) error {
	for code, doc := range permissionDocs {
		if _, err := g.Access.CreatePermission(ctx, code, doc); err != nil {
			return fmt.Errorf("permission %s: %w", code, err)
		}
	}
	if _, err := g.Access.CreateRole(ctx, "moderator", "Moderator", "Moderates posts and comments", false); err != nil &&
		!errors.Is(err, accessdomain.ErrRoleExists) {
		return fmt.Errorf("role moderator: %w", err)
	}
	for role, codes := range rolePermissions {
		for _, code := range codes {
			if err := g.Access.GrantPermission(ctx, role, code, ""); err != nil {
				return fmt.Errorf("grant %s to %s: %w", code, role, err)
			}
		}
	}

	existing, err := g.Access.ListPolicies(ctx)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	for _, op := range ownerPolicies {
		if have[op.name] {
			continue
		}
		p := &accessdomain.Policy{
			Name: op.name, Resource: op.resource, Action: op.action,
			Effect: accessdomain.Allow, Priority: 100, Enabled: true,
			Root: &accessdomain.ConditionGroup{
				Operator: accessdomain.And,
				Conditions: []accessdomain.Condition{
					{Field: "resource.owner_id", Operator: accessdomain.OpEq, Value: []string{"$user.id"}},
				},
			},
		}
		if err := g.Access.SavePolicy(ctx, p); err != nil {
			return fmt.Errorf("policy %q: %w", op.name, err)
		}
	}
	// Policies scoped to roles by a user.roles condition (written by the admin
	// panel before Guard v0.4.0) move to the policy's own role binding.
	if n, err := g.Access.MigrateRoleConditions(ctx); err != nil {
		return fmt.Errorf("migrate role conditions: %w", err)
	} else if n > 0 {
		zap.L().Info("policies bound to roles", zap.Int("migrated", n))
	}
	return g.InvalidateAccess(ctx)
}
