// Package relation is one user blocking or muting another.
//
// A block is mutual in effect: neither side can follow the other, and the
// blocked user cannot comment on the blocker's posts or reply to their
// comments. A mute only silences notifications from the muted user.
package relation

import (
	"context"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

type Kind string

const (
	Block Kind = "block"
	Mute  Kind = "mute"
)

func (k Kind) Valid() bool { return k == Block || k == Mute }

var ErrBlocked = domain.Invalid("you cannot interact with this user")

type Repository interface {
	// Add is idempotent. Blocking also removes follows in both directions.
	Add(ctx context.Context, userID, targetID int, k Kind) error
	Remove(ctx context.Context, userID, targetID int, k Kind) error
	// List returns the users userID blocked or muted, newest first.
	List(ctx context.Context, userID int, k Kind, p domain.Paging) (user.SummaryPage, error)
	// Blocked reports whether either user blocked the other.
	Blocked(ctx context.Context, a, b int) (bool, error)
	// Silenced returns those of recipients who blocked or muted actorID.
	Silenced(ctx context.Context, actorID int, recipients []int) ([]int, error)
}
