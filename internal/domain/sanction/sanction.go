// Package sanction is moderation of accounts: permanent bans and
// time-limited suspensions with a reason.
package sanction

import (
	"context"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

type Kind string

const (
	Banned    Kind = "banned"    // until an admin lifts it
	Suspended Kind = "suspended" // until Until
)

type Sanction struct {
	UserID    int        `json:"user_id"`
	Username  string     `json:"username"`
	Kind      Kind       `json:"kind"`
	Reason    string     `json:"reason"`
	Until     *time.Time `json:"until,omitempty"`
	ActorID   int        `json:"actor_id"`
	CreatedAt time.Time  `json:"created_at"`
}

// Page is one page of sanctions, newest first.
type Page struct {
	Items []Sanction `json:"items"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Limit int        `json:"limit"`
}

// KindOf is Banned without an end date, else Suspended.
func KindOf(until *time.Time) Kind {
	if until == nil {
		return Banned
	}
	return Suspended
}

const maxReason = 500

// New validates a sanction decided at now.
func New(userID, actorID int, reason string, until *time.Time, now time.Time) (Sanction, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return Sanction{}, domain.Invalid("reason required")
	case len(reason) > maxReason:
		return Sanction{}, domain.Invalid("reason too long")
	case until != nil && !until.After(now):
		return Sanction{}, domain.Invalid("until must be in the future")
	case userID == actorID:
		return Sanction{}, domain.Invalid("cannot sanction yourself")
	}
	return Sanction{UserID: userID, ActorID: actorID, Kind: KindOf(until), Reason: reason, Until: until}, nil
}

// Repository returns domain.ErrNotFound when the user has no sanction.
type Repository interface {
	// Save replaces the user's sanction.
	Save(ctx context.Context, s Sanction) (Sanction, error)
	Get(ctx context.Context, userID int) (Sanction, error)
	Delete(ctx context.Context, userID int) error
	// Expired lists suspended users whose end date is before now.
	Expired(ctx context.Context, now time.Time) ([]int, error)
	List(ctx context.Context, p domain.Paging) ([]Sanction, int, error)
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
