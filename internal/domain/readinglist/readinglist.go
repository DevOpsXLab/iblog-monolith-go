// Package readinglist holds a reader's named lists of saved posts.
package readinglist

import (
	"context"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type List struct {
	ID          int    `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	UserID      int    `json:"user_id"`
	Owner       string `json:"owner"`
	Posts       int    `json:"posts"` // published posts in it
	// Contains tells whether the list holds the post asked about
	// (GET /api/me/lists?post_id=).
	Contains  *bool     `json:"contains,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListInput is the editable part of a list.
type ListInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
}

func (in ListInput) Normalize() (ListInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case in.Name == "":
		return in, domain.Invalid("name required")
	case len(in.Name) > 100:
		return in, domain.Invalid("name too long")
	case len(in.Description) > 500:
		return in, domain.Invalid("description too long")
	}
	return in, nil
}

// MaxLists is how many lists one user may have.
const MaxLists = 100

var ErrTooMany = domain.Invalid("too many lists")

// Repository returns domain.ErrNotFound for a missing list or post.
type Repository interface {
	Create(ctx context.Context, userID int, slug string, in ListInput) (List, error)
	Update(ctx context.Context, id int, in ListInput) (List, error)
	Delete(ctx context.Context, id int) error
	GetBySlug(ctx context.Context, slug string) (List, error)
	// ByUser pages the user's lists, newest first; private ones only with
	// private. postID > 0 fills Contains.
	ByUser(ctx context.Context, userID int, private bool, postID int, p domain.Paging) (Page, error)
	Count(ctx context.Context, userID int) (int, error)
	// Add and Remove are idempotent.
	Add(ctx context.Context, id, postID int) error
	Remove(ctx context.Context, id, postID int) error
}

// Page is one page of lists.
type Page struct {
	Items []List `json:"items"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
