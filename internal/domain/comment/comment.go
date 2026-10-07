package comment

import (
	"context"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type Comment struct {
	ID        int        `json:"id"`
	PostID    int        `json:"post_id"`
	ParentID  int        `json:"parent_id"` // 0 = top level
	UserID    int        `json:"user_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	CreatedAt time.Time  `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
	Likes     int        `json:"likes"`
	Liked     bool       `json:"liked" db:"-"` // by the current viewer
}

// Page is one page of comments.
type Page struct {
	Items []Comment `json:"items"`
	Total int       `json:"total"`
	Page  int       `json:"page"`
	Limit int       `json:"limit"`
	// NextCursor fetches the next page with ?cursor=; empty on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

const maxText = 5000

// ValidateText trims and checks comment text.
func ValidateText(text string) (string, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "", domain.Invalid("text required")
	case len(text) > maxText:
		return "", domain.Invalid("text too long")
	}
	return text, nil
}

// New validates input and builds an unsaved comment by a user.
func New(postID, parentID, userID int, author, text string) (Comment, error) {
	text, err := ValidateText(text)
	if err != nil {
		return Comment{}, err
	}
	if parentID < 0 {
		return Comment{}, domain.Invalid("bad parent_id")
	}
	return Comment{PostID: postID, ParentID: parentID, UserID: userID, Author: author, Text: text}, nil
}

var ErrBadParent = domain.Invalid("parent comment not found on this post")

// Repository returns domain.ErrNotFound when the post or comment does not
// exist and ErrBadParent when the parent is not on the same post.
type Repository interface {
	// ListByPost fills Liked for viewerID (0 = anonymous).
	ListByPost(ctx context.Context, postID, viewerID int, p domain.Paging) (Page, error)
	// ListAll is the moderation view, newest first.
	ListAll(ctx context.Context, p domain.Paging) (Page, error)
	Get(ctx context.Context, id int) (Comment, error)
	Add(ctx context.Context, c Comment) (Comment, error)
	Edit(ctx context.Context, id int, text string) (Comment, error)
	Delete(ctx context.Context, id int) error
	// Like and Unlike are idempotent per user; created reports a new like.
	Like(ctx context.Context, id, userID int) (c Comment, created bool, err error)
	Unlike(ctx context.Context, id, userID int) (Comment, error)
}

func (p Page) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, p.NextCursor)
}
