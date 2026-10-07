// Package social holds bookmarks and notifications (follows live in user).
package social

import (
	"context"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
)

type NotificationType string

const (
	NotifyComment     NotificationType = "comment"      // someone commented on your post
	NotifyReply       NotificationType = "reply"        // someone replied to your comment
	NotifyLike        NotificationType = "like"         // someone liked your post
	NotifyCommentLike NotificationType = "comment_like" // someone liked your comment
	NotifyFollow      NotificationType = "follow"       // someone followed you
	NotifyNewPost     NotificationType = "new_post"     // someone you follow published
	NotifyMention     NotificationType = "mention"      // someone @mentioned you in a post or comment
)

type Notification struct {
	ID        int64            `json:"id"`
	UserID    int              `json:"user_id"`
	Type      NotificationType `json:"type"`
	ActorID   int              `json:"actor_id"`
	Actor     string           `json:"actor"`
	PostID    int              `json:"post_id"`
	CommentID int              `json:"comment_id"`
	ReadAt    *time.Time       `json:"read_at,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

type NotificationPage struct {
	Items  []Notification `json:"items"`
	Total  int            `json:"total"`
	Unread int            `json:"unread"`
	Page   int            `json:"page"`
	Limit  int            `json:"limit"`
	// NextCursor fetches the next page with ?cursor=; empty on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// HideKind is what a reader asked to see less of.
type HideKind string

const (
	HidePost   HideKind = "post"
	HideAuthor HideKind = "author"
	HideTag    HideKind = "tag"
)

func (k HideKind) Valid() bool { return k == HidePost || k == HideAuthor || k == HideTag }

// Hide keeps a post, an author or a tag out of a reader's feeds. Target is
// the post id, the author's username or the tag.
type Hide struct {
	Kind      HideKind  `json:"kind"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository returns domain.ErrNotFound for a missing post or user.
type Repository interface {
	Bookmark(ctx context.Context, userID, postID int) error
	Unbookmark(ctx context.Context, userID, postID int) error

	// FollowTag and UnfollowTag are idempotent.
	FollowTag(ctx context.Context, userID int, tag string) error
	UnfollowTag(ctx context.Context, userID int, tag string) error
	FollowedTags(ctx context.Context, userID int) ([]string, error)

	// Notify stores notifications for every recipient and returns them.
	Notify(ctx context.Context, n Notification, recipients []int) ([]Notification, error)
	Notifications(ctx context.Context, userID int, p domain.Paging) (NotificationPage, error)
	// Hide and Unhide are idempotent; target is a post id, user id or tag.
	Hide(ctx context.Context, userID int, k HideKind, target string) error
	Unhide(ctx context.Context, userID int, k HideKind, target string) error
	// Hidden pages the user's hides, newest first, authors by username.
	Hidden(ctx context.Context, userID int, p domain.Paging) (HidePage, error)
	CountHidden(ctx context.Context, userID int) (int, error)

	// MarkRead marks ids as read; empty ids marks nothing.
	MarkRead(ctx context.Context, userID int, ids []int64) error
	// MarkAllRead marks every unread notification as read.
	MarkAllRead(ctx context.Context, userID int) error
}

// NotificationMeta adds the unread count to the page metadata.
type NotificationMeta struct {
	domain.Meta
	Unread int `json:"unread"`
}

func (p NotificationPage) Paginated() (any, any) {
	return p.Items, NotificationMeta{domain.NewMeta(p.Page, p.Limit, p.Total, p.NextCursor), p.Unread}
}

// HidePage is one page of a reader's hides.
type HidePage struct {
	Items []Hide `json:"items"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
}

func (p HidePage) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "")
}
