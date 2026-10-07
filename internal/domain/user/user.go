// Package user is the blog's view of a person: public identity, profile,
// follows and the account lifecycle. Credentials, sessions and roles live in
// Guard.
package user

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/bakhod1r/emailx"
)

// DeletionGrace is how long a deleted account can be restored by logging in
// before it is purged.
const DeletionGrace = 30 * 24 * time.Hour

type User struct {
	ID            int        `json:"id"`
	Username      string     `json:"username"`
	Email         string     `json:"email,omitempty"`
	EmailVerified bool       `json:"email_verified"`
	DisplayName   string     `json:"display_name"`
	Bio           string     `json:"bio"`
	AvatarURL     string     `json:"avatar_url"`
	Roles         []string   `json:"roles"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Profile is the public view of a user.
type Profile struct {
	ID          int       `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
	Posts       int       `json:"posts"` // published only
	Likes       int       `json:"likes"`
	Followers   int       `json:"followers"`
	Following   int       `json:"following"`
	IsFollowing bool      `json:"is_following"` // by the current viewer
	// IsSubscribed: the viewer gets an email when this user publishes.
	IsSubscribed bool `json:"is_subscribed"`
	// PinnedPostID is the story shown first on the profile (0 = none).
	PinnedPostID int `json:"pinned_post_id"`
}

// Subscriber is who gets an email about a new post.
type Subscriber struct {
	ID    int
	Email string
}

// Summary is a user in follower/following lists.
type Summary struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

// SummaryPage is one page of users.
type SummaryPage struct {
	Items []Summary `json:"items"`
	Total int       `json:"total"` // with a cursor: users from the cursor on
	Page  int       `json:"page"`
	Limit int       `json:"limit"`
	// NextCursor fetches the next page with ?cursor=; empty on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Page is one page of full users (admin view).
type Page struct {
	Items []User `json:"items"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
}

// ProfileUpdate is the editable part of a user's profile.
type ProfileUpdate struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURL   string `json:"avatar_url"`
}

// Normalize validates u and trims it.
func (u ProfileUpdate) Normalize() (ProfileUpdate, error) {
	u.DisplayName = strings.TrimSpace(u.DisplayName)
	u.Bio = strings.TrimSpace(u.Bio)
	u.AvatarURL = strings.TrimSpace(u.AvatarURL)
	switch {
	case len(u.DisplayName) > 64:
		return u, domain.Invalid("display_name too long")
	case len(u.Bio) > 300:
		return u, domain.Invalid("bio too long")
	case u.AvatarURL != "" && !strings.HasPrefix(u.AvatarURL, "/api/uploads/") &&
		!strings.HasPrefix(u.AvatarURL, "https://") && !strings.HasPrefix(u.AvatarURL, "http://"):
		return u, domain.Invalid("bad avatar_url")
	}
	return u, nil
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// Registration is validated sign-up input.
type Registration struct {
	Username string
	Email    string
	Password string
}

// NewRegistration validates sign-up input.
func NewRegistration(username, email, password string) (Registration, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return Registration{}, domain.Invalid("username: 3-32 chars, letters, digits, _")
	}
	addr, err := ValidateEmail(email)
	if err != nil {
		return Registration{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return Registration{}, err
	}
	return Registration{Username: username, Email: addr, Password: password}, nil
}

// ValidateEmail checks syntax with emailx and rejects disposable mailboxes.
func ValidateEmail(raw string) (string, error) {
	e, err := emailx.Parse(strings.TrimSpace(raw))
	if err != nil || e.ValidateSyntax() != nil {
		return "", domain.Invalid("invalid email")
	}
	if e.IsDisposable() {
		return "", domain.Invalid("disposable email addresses are not allowed")
	}
	return e.Address(), nil
}

func ValidatePassword(p string) error {
	switch {
	case len(p) < 8:
		return domain.Invalid("password: at least 8 chars")
	case len(p) > 72:
		return domain.Invalid("password: at most 72 bytes")
	}
	return nil
}

// Repository returns domain.ErrConflict on a duplicate username or email
// (case-insensitive) and domain.ErrNotFound for a missing user. Users with a
// pending deletion are hidden from public reads (Profile, follows).
type Repository interface {
	Create(ctx context.Context, username, email string) (User, error)
	// Ensure returns the user with this username, creating it when missing.
	Ensure(ctx context.Context, username, email string) (User, error)
	MarkEmailVerified(ctx context.Context, id int) error
	Delete(ctx context.Context, id int) error
	GetByID(ctx context.Context, id int) (User, error)
	GetByUsername(ctx context.Context, username string) (User, error)
	// GetByLogin finds a user by username or email.
	GetByLogin(ctx context.Context, login string) (User, error)
	List(ctx context.Context, query string, p domain.Paging) (Page, error)

	// Profile fills Profile.IsFollowing for viewerID (0 = anonymous).
	Profile(ctx context.Context, username string, viewerID int) (Profile, error)
	UpdateProfile(ctx context.Context, id int, u ProfileUpdate) (User, error)

	// Follow reports whether the follow is new. Follow and Unfollow are idempotent.
	Follow(ctx context.Context, followerID, followeeID int) (bool, error)
	Unfollow(ctx context.Context, followerID, followeeID int) error
	Followers(ctx context.Context, id int, p domain.Paging) (SummaryPage, error)
	Following(ctx context.Context, id int, p domain.Paging) (SummaryPage, error)
	FollowerIDs(ctx context.Context, id int) ([]int, error)

	// SetPinned pins postID (0 unpins) on the user's profile.
	SetPinned(ctx context.Context, id, postID int) error

	// Subscribe and Unsubscribe are idempotent.
	Subscribe(ctx context.Context, subscriberID, authorID int) error
	Unsubscribe(ctx context.Context, subscriberID, authorID int) error
	// Subscribers are active users with a verified email subscribed to authorID.
	Subscribers(ctx context.Context, authorID int) ([]Subscriber, error)

	MarkDeleted(ctx context.Context, id int, at time.Time) error
	Restore(ctx context.Context, id int) error
	// DuePurges lists users whose deletion grace period ended before cutoff.
	DuePurges(ctx context.Context, cutoff time.Time) ([]int, error)
	// Purge removes the user and everything they wrote if their deletion was
	// requested before cutoff. It reports whether anything was removed.
	Purge(ctx context.Context, id int, cutoff time.Time) (bool, error)
}

func (p SummaryPage) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, p.NextCursor)
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
