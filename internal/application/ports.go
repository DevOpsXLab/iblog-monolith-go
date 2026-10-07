// Package application holds use cases. It enforces permissions through the
// Authorizer port and orchestrates domain rules and repositories; it knows
// nothing about HTTP, SQL, Redis or Guard.
package application

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

// Authorizer decides with RBAC + ABAC whether the caller in ctx may perform
// action on a resource owned by ownerID. It returns nil,
// domain.ErrUnauthorized (no caller) or domain.ErrForbidden.
type Authorizer interface {
	Can(ctx context.Context, resource, action string, id, ownerID int) error
}

// Auditor records who did what. It never fails the caller.
type Auditor interface {
	Record(ctx context.Context, actorID int, action, target string, meta map[string]any)
}

// Jobs schedules background work.
type Jobs interface {
	PublishPostAt(ctx context.Context, postID int, at time.Time) error
	FanoutNewPost(ctx context.Context, postID int) error
	PurgeAccountAt(ctx context.Context, userID int, at time.Time) error
	SendEmail(ctx context.Context, m Email) error
	// SendEmailOnce enqueues at most one email per id, so a repeated call
	// does not send it twice. (Retries of any email task are deduped by the
	// worker.)
	SendEmailOnce(ctx context.Context, id string, m Email) error
	Thumbnail(ctx context.Context, key string) error
}

// Events publishes realtime messages (SSE fan-out).
type Events interface {
	Publish(ctx context.Context, channel string, v any) error
	// Subscribe delivers messages until ctx ends.
	Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
}

// Channel names.
func NotificationsChannel(userID int) string { return "notifications:" + itoa(userID) }
func CommentsChannel(postID int) string      { return "comments:" + itoa(postID) }

// Markdown renders a Markdown body to sanitized HTML.
type Markdown interface {
	Render(body string) string
}

// ViewCounter counts unique post views and ranks trending posts.
type ViewCounter interface {
	// Hit counts viewer once per post; viewer is a user or IP key.
	Hit(ctx context.Context, postID int, viewer string) error
	Count(ctx context.Context, postID int) int64
	// Counts is Count for many posts in one round trip.
	Counts(ctx context.Context, postIDs []int) map[int]int64
	Trending(ctx context.Context, days, limit int) ([]int, error)
	// Daily returns unique views of the post on each of days (UTC).
	Daily(ctx context.Context, postID int, days []time.Time) ([]int64, error)
}

// ThumbKey is the storage key of an upload's thumbnail.
func ThumbKey(key string) string { return "thumb/" + keyBase(key) + ".jpg" }

// VariantWidths are the widths of an upload's WebP variants, for srcset.
var VariantWidths = []int{320, 640, 1280}

// VariantKey is the storage key of an upload's WebP variant of width w.
func VariantKey(key string, w int) string { return "img/" + keyBase(key) + "-" + itoa(w) + ".webp" }

func keyBase(key string) string {
	if i := strings.LastIndexByte(key, '.'); i > 0 {
		return key[:i]
	}
	return key
}

type Email struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	// Unsubscribe is a one-click unsubscribe URL (List-Unsubscribe, RFC 8058).
	Unsubscribe string `json:"unsubscribe,omitempty"`
}

type Mailer interface {
	Send(ctx context.Context, m Email) error
}

// Storage keeps uploaded files.
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (Object, error)
}

type Object struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
}

// Meta describes the client of a request, for sessions and audit.
type Meta struct {
	IP        string
	UserAgent string
}

// Session is a logged-in session: send Token as "Authorization: Bearer".
//
// With two-factor authentication on, login returns only MFARequired and
// MFAToken; POST /api/auth/login/2fa with the token and a code finishes it.
type Session struct {
	Token       string    `json:"token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitzero"`
	User        user.User `json:"user,omitzero"`
	MFARequired bool      `json:"mfa_required,omitempty"`
	MFAToken    string    `json:"mfa_token,omitempty"`
}

// Credentials is a session token as issued by the identity provider.
type Credentials struct {
	Token     string
	ExpiresAt time.Time
}

// Identity is the credential and session store (Guard).
type Identity interface {
	CreateAccount(ctx context.Context, userID int, email, password string) error
	EnsureSuperAdmin(ctx context.Context, userID int, email, password string) error
	Login(ctx context.Context, email, password string, m Meta) (Credentials, error)
	// Refresh rotates a session: the old token stops working.
	Refresh(ctx context.Context, token string) (Credentials, error)
	Logout(ctx context.Context, token string, m Meta) error
	LogoutAll(ctx context.Context, userID int) error
	// Authenticate resolves a token to its user id.
	Authenticate(ctx context.Context, token string) (int, error)
	VerifyPassword(ctx context.Context, email, password string) error
	ChangePassword(ctx context.Context, userID int, oldPassword, newPassword string) error
	// SetPassword replaces the password and ends every session.
	SetPassword(ctx context.Context, userID int, password string) error
	Roles(ctx context.Context, userID int) ([]string, error)

	// StartSession opens a session without a password, after the caller
	// has verified every factor itself (two-factor login).
	StartSession(ctx context.Context, userID int, m Meta) (Credentials, error)
	Sessions(ctx context.Context, userID int) ([]SessionInfo, error)
	// RevokeSession ends one of userID's sessions (domain.ErrNotFound if
	// it is not theirs).
	RevokeSession(ctx context.Context, userID int, sessionID string) error
	// SessionID is the id of the session behind token.
	SessionID(ctx context.Context, token string) (string, error)
	// SetBlocked bans (true) or reactivates (false) an account; blocking
	// ends every session. actorID 0 is the system (expired suspensions).
	SetBlocked(ctx context.Context, actorID, userID int, blocked bool) error
}

// SessionInfo is one logged-in device.
type SessionInfo struct {
	ID         string    `json:"id"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// MFAChallenges hold the state between a correct password and the second
// factor during login.
type MFAChallenges interface {
	Issue(ctx context.Context, userID int, ttl time.Duration) (string, error)
	// Attempt counts a guess and returns the user id, or an error once the
	// challenge expired or ran out of attempts.
	Attempt(ctx context.Context, token string) (int, error)
	Consume(ctx context.Context, token string) error
}

// SecretBox encrypts secrets at rest.
type SecretBox interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// SuggestedUser is a who-to-follow suggestion.
type SuggestedUser struct {
	user.Summary
	Followers     int    `json:"followers"`
	MutualFollows int    `json:"mutual_follows"` // people you follow who follow them
	Reason        string `json:"reason"`         // followed_by_people_you_follow, writes_about_your_tags, popular
}

// Discovery answers recommendation queries.
type Discovery interface {
	// RelatedPosts returns ids of published posts similar to postID.
	RelatedPosts(ctx context.Context, postID, limit int) ([]int, error)
	SuggestUsers(ctx context.Context, userID, limit int) ([]SuggestedUser, error)
	// UserIDs resolves usernames (lower case) to active user ids.
	UserIDs(ctx context.Context, usernames []string) ([]int, error)
	// ForYou returns ids of published posts ranked for userID's home feed.
	// total counts every candidate (0 on a page past the end).
	ForYou(ctx context.Context, userID, limit, offset int) (ids []int, total int, err error)
}

// Imported is a story read from a web page, its body as Markdown.
type Imported struct {
	Title        string
	Subtitle     string
	Body         string
	CoverURL     string
	CanonicalURL string
}

// Importer fetches a public web page and extracts its story.
type Importer interface {
	Fetch(ctx context.Context, url string) (Imported, error)
}

// ResetTokens are single-use password reset tokens.
type ResetTokens interface {
	Issue(ctx context.Context, userID int, ttl time.Duration) (string, error)
	// Consume returns the user id and invalidates the token.
	Consume(ctx context.Context, token string) (int, error)
}
