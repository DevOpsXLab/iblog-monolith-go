// Package series groups an author's posts into ordered parts.
package series

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
)

type Series struct {
	ID          int       `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	UserID      int       `json:"user_id"`
	Author      string    `json:"author"`
	Parts       int       `json:"parts"` // visible to the viewer
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Part is a post in a series; Position starts at 1.
type Part struct {
	Position    int         `json:"position"`
	PostID      int         `json:"post_id"`
	Title       string      `json:"title"`
	Slug        string      `json:"slug"`
	Status      post.Status `json:"status"`
	PublishedAt *time.Time  `json:"published_at"`
}

// Detail is a series with its parts in order.
type Detail struct {
	Series
	Posts []Part `json:"posts"`
}

// Navigation is a post's place in its series.
type Navigation struct {
	Series   Series `json:"series"`
	Position int    `json:"position"`
	Prev     *Part  `json:"prev"`
	Next     *Part  `json:"next"`
	Posts    []Part `json:"posts"`
}

// Navigate locates postID among parts.
func Navigate(s Series, parts []Part, postID int) (Navigation, bool) {
	i := slices.IndexFunc(parts, func(p Part) bool { return p.PostID == postID })
	if i < 0 {
		return Navigation{}, false
	}
	n := Navigation{Series: s, Position: parts[i].Position, Posts: parts}
	if i > 0 {
		n.Prev = &parts[i-1]
	}
	if i < len(parts)-1 {
		n.Next = &parts[i+1]
	}
	return n, true
}

// SeriesInput is the editable part of a series.
type SeriesInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (in SeriesInput) Normalize() (SeriesInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case in.Title == "":
		return in, domain.Invalid("title required")
	case len(in.Title) > 200:
		return in, domain.Invalid("title too long")
	case len(in.Description) > 1000:
		return in, domain.Invalid("description too long")
	}
	return in, nil
}

const MaxParts = 100

// ValidateOrder checks a new list of post ids.
func ValidateOrder(ids []int) error {
	if len(ids) > MaxParts {
		return domain.Invalid("too many posts")
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return domain.Invalid("post_ids must be distinct post ids")
		}
		seen[id] = true
	}
	return nil
}

var ErrPostInOtherSeries = domain.Invalid("a post is already in another series")

// Repository returns domain.ErrNotFound for a missing series.
type Repository interface {
	Create(ctx context.Context, userID int, slug string, in SeriesInput) (Series, error)
	Update(ctx context.Context, id int, in SeriesInput) (Series, error)
	Delete(ctx context.Context, id int) error
	GetBySlug(ctx context.Context, slug string) (Series, error)
	// ByUser lists the user's series, newest first.
	ByUser(ctx context.Context, userID int) ([]Series, error)
	// Parts lists the series' posts in order; published only unless all.
	Parts(ctx context.Context, id int, all bool) ([]Part, error)
	// SetPosts replaces the parts; every post must belong to ownerID
	// (else ErrNotFound) and no other series (ErrPostInOtherSeries).
	SetPosts(ctx context.Context, id, ownerID int, postIDs []int) error
	// OfPost returns the series a post belongs to.
	OfPost(ctx context.Context, postID int) (Series, error)
}
