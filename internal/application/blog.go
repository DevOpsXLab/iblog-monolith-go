package application

import (
	"context"
	"errors"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/category"
	"github.com/iBlog/iblog-monolith-go/internal/domain/comment"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/publication"
	"github.com/iBlog/iblog-monolith-go/internal/domain/readinglist"
	"github.com/iBlog/iblog-monolith-go/internal/domain/relation"
	"github.com/iBlog/iblog-monolith-go/internal/domain/report"
	"github.com/iBlog/iblog-monolith-go/internal/domain/revision"
	"github.com/iBlog/iblog-monolith-go/internal/domain/series"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

func itoa(n int) string { return strconv.Itoa(n) }

type Stats struct {
	Posts      int `json:"posts"`
	Comments   int `json:"comments"`
	Likes      int `json:"likes"`
	Categories int `json:"categories"`
	Users      int `json:"users"`
}

type StatsReader interface {
	Stats(ctx context.Context) (Stats, error)
}

// Repos groups the repositories Blog needs.
type Repos struct {
	Posts        post.Repository
	Comments     comment.Repository
	Categories   category.Repository
	Users        user.Repository
	Social       social.Repository
	Reports      report.Repository
	Publications publication.Repository
	Stats        StatsReader
	// Optional: blocks/mutes, revisions, series and recommendations.
	Relations relation.Repository
	Revisions revision.Repository
	Series    series.Repository
	Discovery Discovery
	Lists     readinglist.Repository
}

// Ports groups the side-effect ports Blog needs.
type Ports struct {
	Authz  Authorizer
	Audit  Auditor
	Jobs   Jobs
	Events Events
	Views  ViewCounter
	// Markdown renders post bodies to sanitized HTML (optional).
	Markdown Markdown
	// Importer reads stories from other sites (optional).
	Importer Importer
}

type Blog struct {
	Repos
	Ports
	// RequireVerifiedEmail blocks posting, commenting and creating
	// publications until the user confirms their email.
	RequireVerifiedEmail bool
	// SiteURL is the client site, for links in email.
	SiteURL string
	// APIURL is this API's public URL, for one-click unsubscribe links.
	APIURL string
	// UnsubscribeKey signs unsubscribe links.
	UnsubscribeKey []byte
	rendered       *renderCache
	now            func() time.Time
}

// requireVerified enforces RequireVerifiedEmail for a write by actor.
func (b *Blog) requireVerified(actor user.User) error {
	if b.RequireVerifiedEmail && !actor.EmailVerified {
		return domain.ErrEmailNotVerified
	}
	return nil
}

func NewBlog(r Repos, p Ports) *Blog {
	return &Blog{Repos: r, Ports: p, now: time.Now, rendered: newRenderCache(2000)}
}

// Posts

func (b *Blog) ListPosts(ctx context.Context, f post.Filter) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListPosts")
	defer span.End()
	f.Status = post.StatusPublished
	return b.Posts.List(ctx, f)
}

// AdminPosts lists everyone's posts in one status (empty = published).
// Drafts and scheduled posts need post.read_draft as a role permission; the
// ownership policy does not apply because the list has no single owner.
func (b *Blog) AdminPosts(ctx context.Context, f post.Filter) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.AdminPosts")
	defer span.End()
	switch f.Status {
	case "":
		f.Status = post.StatusPublished
	case post.StatusPublished, post.StatusUnlisted, post.StatusDraft, post.StatusScheduled:
	default:
		return post.Page{}, domain.Invalid("status must be draft, scheduled, published or unlisted")
	}
	if err := b.Authz.Can(ctx, "post", "read_draft", 0, 0); err != nil {
		return post.Page{}, err
	}
	return b.Posts.List(ctx, f)
}

// MyPosts lists the actor's posts in any status (empty = all published).
func (b *Blog) MyPosts(ctx context.Context, actor user.User, status post.Status, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyPosts")
	defer span.End()
	if status == "" {
		status = post.StatusPublished
	}
	return b.Posts.List(ctx, post.Filter{UserID: actor.ID, Status: status, Paging: p})
}

func (b *Blog) Feed(ctx context.Context, actor user.User, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.Feed")
	defer span.End()
	return b.Posts.List(ctx, post.Filter{FeedOf: actor.ID, HideFor: actor.ID, Status: post.StatusPublished, Paging: p})
}

func (b *Blog) Bookmarks(ctx context.Context, actor user.User, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.Bookmarks")
	defer span.End()
	return b.Posts.List(ctx, post.Filter{BookmarkedBy: actor.ID, Status: post.StatusPublished, Paging: p})
}

// GetPost returns a post the viewer may read, with body_html rendered.
func (b *Blog) GetPost(ctx context.Context, id, viewerID int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetPost")
	defer span.End()
	p, err := b.readable(ctx, id, viewerID)
	return b.render(p), err
}

func (b *Blog) GetPostBySlug(ctx context.Context, slug string, viewerID int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetPostBySlug")
	defer span.End()
	p, err := b.Posts.GetBySlug(ctx, slug, viewerID)
	p, err = b.visible(ctx, p, err)
	return b.render(p), err
}

// readable is GetPost without rendering, for permission checks.
func (b *Blog) readable(ctx context.Context, id, viewerID int) (post.Post, error) {
	p, err := b.Posts.Get(ctx, id, viewerID)
	return b.visible(ctx, p, err)
}

func (b *Blog) render(p post.Post) post.Post {
	if b.Markdown != nil && p.ID != 0 {
		p.BodyHTML = b.rendered.render(b.Markdown, p.Body)
	}
	return p
}

// visible hides drafts and scheduled posts from everyone but their author
// and roles allowed to read drafts; it also fills the view count.
func (b *Blog) visible(ctx context.Context, p post.Post, err error) (post.Post, error) {
	if err != nil {
		return p, err
	}
	if !p.IsReadable() {
		if b.Authz.Can(ctx, "post", "read_draft", p.ID, p.UserID) != nil {
			return post.Post{}, domain.ErrNotFound
		}
	}
	p.Views = b.Views.Count(ctx, p.ID)
	return p, nil
}

func (b *Blog) CreatePost(ctx context.Context, actor user.User, d post.Draft) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreatePost")
	defer span.End()
	if err := b.Authz.Can(ctx, "post", "create", 0, 0); err != nil {
		return post.Post{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return post.Post{}, err
	}
	d, err := d.Normalize(b.now())
	if err != nil {
		return post.Post{}, err
	}
	if err := b.requireWriter(ctx, actor, d.PublicationID); err != nil {
		return post.Post{}, err
	}
	p, err := b.Posts.Create(ctx, d, actor.ID, actor.Username)
	if err != nil {
		return p, err
	}
	b.afterSave(ctx, post.Post{}, p)
	return p, nil
}

// UpdatePost is allowed for the author (ABAC), roles with post.update and
// editors of the post's publication. Moving a post into a publication needs
// membership there.
func (b *Blog) UpdatePost(ctx context.Context, actor user.User, id int, d post.Draft) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdatePost")
	defer span.End()
	d, err := d.Normalize(b.now())
	if err != nil {
		return post.Post{}, err
	}
	before, err := b.Posts.Get(ctx, id, 0)
	if err != nil {
		return before, err
	}
	if err := b.Authz.Can(ctx, "post", "update", id, before.UserID); err != nil {
		if !errors.Is(err, domain.ErrForbidden) || !b.isPublicationEditor(ctx, actor, before.PublicationID) {
			return post.Post{}, err
		}
	}
	if d.PublicationID != before.PublicationID {
		if err := b.requireWriter(ctx, actor, d.PublicationID); err != nil {
			return post.Post{}, err
		}
	}
	p, err := b.updateWithRevision(ctx, actor, before, d)
	if err != nil {
		return p, err
	}
	b.afterSave(ctx, before, p)
	return p, nil
}

// afterSave schedules publishing or announces a fresh publication.
func (b *Blog) afterSave(ctx context.Context, before, p post.Post) {
	switch {
	case p.Status == post.StatusScheduled && p.PublishAt != nil:
		if err := b.Jobs.PublishPostAt(ctx, p.ID, *p.PublishAt); err != nil {
			zap.L().Error("schedule publish", zap.Any("ctx", ctx), zap.Int("post", p.ID), zap.Error(err))
		}
	case p.IsPublic() && !before.IsPublic():
		if err := b.Jobs.FanoutNewPost(ctx, p.ID); err != nil {
			zap.L().Error("fanout", zap.Any("ctx", ctx), zap.Int("post", p.ID), zap.Error(err))
		}
	}
	if p.IsPublic() {
		old := ""
		if before.IsPublic() {
			old = before.Body
		}
		b.mentionInPost(ctx, p, old)
	}
}

// DeletePost is allowed for the author (ABAC) and roles with post.delete.
func (b *Blog) DeletePost(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeletePost")
	defer span.End()
	p, err := b.Posts.Get(ctx, id, 0)
	if err != nil {
		return err
	}
	if err := b.Authz.Can(ctx, "post", "delete", id, p.UserID); err != nil {
		return err
	}
	if err := b.Posts.Delete(ctx, id); err != nil {
		return err
	}
	b.Audit.Record(ctx, actor.ID, "post.delete", "post:"+itoa(id), map[string]any{"title": p.Title, "owner_id": p.UserID})
	return nil
}

// PublishScheduled publishes one scheduled post if it is due (job handler).
func (b *Blog) PublishScheduled(ctx context.Context, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.PublishScheduled")
	defer span.End()
	ok, err := b.Posts.Publish(ctx, id, b.now())
	if err != nil {
		return err
	}
	if !ok {
		// Not flipped here: not due yet, or already published by an earlier
		// attempt that then failed to enqueue the fan-out. Enqueue again in
		// the latter case; the fan-out TaskID dedupes.
		p, err := b.Posts.Get(ctx, id, 0)
		if err != nil || !p.IsPublic() {
			return ignoreNotFound(err)
		}
		return b.Jobs.FanoutNewPost(ctx, id)
	}
	b.Audit.Record(ctx, 0, "post.publish_scheduled", "post:"+itoa(id), nil)
	if p, err := b.Posts.Get(ctx, id, 0); err == nil {
		b.mentionInPost(ctx, p, "")
	}
	return b.Jobs.FanoutNewPost(ctx, id)
}

// PublishDue publishes every overdue scheduled post (periodic safety net).
func (b *Blog) PublishDue(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Blog.PublishDue")
	defer span.End()
	ids, err := b.Posts.DueScheduled(ctx, b.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		errs = append(errs, b.PublishScheduled(ctx, id))
	}
	return errors.Join(errs...)
}

// FanoutNewPost notifies the author's followers (job handler).
func (b *Blog) FanoutNewPost(ctx context.Context, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.FanoutNewPost")
	defer span.End()
	p, err := b.Posts.Get(ctx, id, 0)
	if err != nil || !p.IsPublic() || p.UserID == 0 {
		return ignoreNotFound(err)
	}
	followers, err := b.Users.FollowerIDs(ctx, p.UserID)
	if err != nil {
		return err
	}
	err = b.notify(ctx, social.Notification{Type: social.NotifyNewPost, ActorID: p.UserID, PostID: p.ID}, followers...)
	// Email enqueues are deduped per subscriber, but a retry of this task would
	// re-send the in-app notifications above; log email failures instead.
	if eerr := b.emailSubscribers(ctx, p); eerr != nil {
		zap.L().Error("new post emails", zap.Any("ctx", ctx), zap.Int("post", p.ID), zap.Error(eerr))
	}
	return err
}

func (b *Blog) LikePost(ctx context.Context, actor user.User, id int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.LikePost")
	defer span.End()
	if err := b.Authz.Can(ctx, "like", "create", id, 0); err != nil {
		return post.Post{}, err
	}
	if _, err := b.readable(ctx, id, actor.ID); err != nil {
		return post.Post{}, err
	}
	p, created, err := b.Posts.Like(ctx, id, actor.ID)
	if err == nil && created {
		b.notifyLogged(ctx, social.Notification{Type: social.NotifyLike, ActorID: actor.ID, PostID: id}, p.UserID)
	}
	return p, err
}

// ClapPost adds n claps (capped at post.MaxClaps per reader); the author is
// notified on the reader's first clap only.
func (b *Blog) ClapPost(ctx context.Context, actor user.User, id, n int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.ClapPost")
	defer span.End()
	if err := post.ValidateClaps(n); err != nil {
		return post.Post{}, err
	}
	if err := b.Authz.Can(ctx, "like", "create", id, 0); err != nil {
		return post.Post{}, err
	}
	if _, err := b.readable(ctx, id, actor.ID); err != nil {
		return post.Post{}, err
	}
	p, created, err := b.Posts.Clap(ctx, id, actor.ID, n)
	if err == nil && created {
		b.notifyLogged(ctx, social.Notification{Type: social.NotifyLike, ActorID: actor.ID, PostID: id}, p.UserID)
	}
	return p, err
}

func (b *Blog) UnlikePost(ctx context.Context, actor user.User, id int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.UnlikePost")
	defer span.End()
	if err := b.Authz.Can(ctx, "like", "create", id, 0); err != nil {
		return post.Post{}, err
	}
	if _, err := b.readable(ctx, id, actor.ID); err != nil {
		return post.Post{}, err
	}
	return b.Posts.Unlike(ctx, id, actor.ID)
}

// ViewPost counts a unique view by viewer (user id or IP).
func (b *Blog) ViewPost(ctx context.Context, id int, viewer string) error {
	ctx, span := tracer.Start(ctx, "Blog.ViewPost")
	defer span.End()
	p, err := b.Posts.Get(ctx, id, 0)
	if err != nil {
		return err
	}
	if !p.IsReadable() {
		return domain.ErrNotFound
	}
	return b.Views.Hit(ctx, id, viewer)
}

func (b *Blog) Trending(ctx context.Context, days, limit int) ([]post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.Trending")
	defer span.End()
	days, limit = min(max(days, 1), 30), min(max(limit, 1), 50)
	ids, err := b.Views.Trending(ctx, days, limit)
	if err != nil || len(ids) == 0 {
		return []post.Post{}, err
	}
	return b.Posts.ListByIDs(ctx, ids)
}

func (b *Blog) Tags(ctx context.Context) ([]post.TagCount, error) {
	ctx, span := tracer.Start(ctx, "Blog.Tags")
	defer span.End()
	return b.Posts.Tags(ctx)
}

// Highlights

const topHighlights = 5

func (b *Blog) PostHighlights(ctx context.Context, postID, viewerID int) (post.Highlights, error) {
	ctx, span := tracer.Start(ctx, "Blog.PostHighlights")
	defer span.End()
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return post.Highlights{}, err
	}
	return b.Posts.Highlights(ctx, postID, viewerID, topHighlights)
}

func (b *Blog) AddHighlight(ctx context.Context, actor user.User, postID, start, end int) (post.Highlight, error) {
	ctx, span := tracer.Start(ctx, "Blog.AddHighlight")
	defer span.End()
	if err := b.Authz.Can(ctx, "highlight", "create", postID, 0); err != nil {
		return post.Highlight{}, err
	}
	p, err := b.readable(ctx, postID, actor.ID)
	if err != nil {
		return post.Highlight{}, err
	}
	h, err := post.NewHighlight(p.Body, postID, actor.ID, start, end)
	if err != nil {
		return h, err
	}
	return b.Posts.AddHighlight(ctx, h)
}

// DeleteHighlight is allowed for its owner and holders of highlight.delete.
func (b *Blog) DeleteHighlight(ctx context.Context, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteHighlight")
	defer span.End()
	h, err := b.Posts.GetHighlight(ctx, id)
	if err != nil {
		return err
	}
	if err := b.Authz.Can(ctx, "highlight", "delete", id, h.UserID); err != nil {
		return err
	}
	return b.Posts.DeleteHighlight(ctx, id)
}

// Author stats

type AuthorStats struct {
	Items  []post.AuthorStat `json:"items"`
	Total  int               `json:"total"`
	Page   int               `json:"page"`
	Limit  int               `json:"limit"`
	Totals struct {
		Views    int64 `json:"views"`
		Claps    int   `json:"claps"`
		Comments int   `json:"comments"`
		Reads    int   `json:"reads"`
	} `json:"totals"` // over this page
}

// StatsMeta adds totals over the page to the page metadata.
type StatsMeta struct {
	domain.Meta
	Totals any `json:"totals"`
}

func (s AuthorStats) Paginated() (any, any) {
	return s.Items, StatsMeta{domain.NewMeta(s.Page, s.Limit, s.Total, ""), s.Totals}
}

// MyStats lists the caller's posts with views, claps, comments, bookmarks
// and highlights.
func (b *Blog) MyStats(ctx context.Context, actor user.User, p domain.Paging) (AuthorStats, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyStats")
	defer span.End()
	items, total, err := b.Posts.AuthorStats(ctx, actor.ID, p)
	out := AuthorStats{Items: items, Total: total, Page: p.Page, Limit: p.Limit}
	if err != nil {
		return out, err
	}
	ids := make([]int, len(out.Items))
	for i, s := range out.Items {
		ids[i] = s.PostID
	}
	views := b.Views.Counts(ctx, ids)
	for i := range out.Items {
		s := &out.Items[i]
		s.Views = views[s.PostID]
		out.Totals.Views += s.Views
		out.Totals.Claps += s.Claps
		out.Totals.Comments += s.Comments
		out.Totals.Reads += s.Reads
		if s.Views > 0 {
			s.ReadRatio = min(1, float64(s.Reads)/float64(s.Views))
		}
	}
	return out, nil
}

// Tag follows

func (b *Blog) FollowTag(ctx context.Context, actor user.User, tag string) error {
	ctx, span := tracer.Start(ctx, "Blog.FollowTag")
	defer span.End()
	if err := b.Authz.Can(ctx, "follow", "create", 0, 0); err != nil {
		return err
	}
	tag, err := post.ValidateTag(tag)
	if err != nil {
		return err
	}
	return b.Social.FollowTag(ctx, actor.ID, tag)
}

func (b *Blog) UnfollowTag(ctx context.Context, actor user.User, tag string) error {
	ctx, span := tracer.Start(ctx, "Blog.UnfollowTag")
	defer span.End()
	return b.Social.UnfollowTag(ctx, actor.ID, post.NormalizeTag(tag))
}

func (b *Blog) FollowedTags(ctx context.Context, actor user.User) ([]string, error) {
	ctx, span := tracer.Start(ctx, "Blog.FollowedTags")
	defer span.End()
	return b.Social.FollowedTags(ctx, actor.ID)
}

// Bookmarks

func (b *Blog) Bookmark(ctx context.Context, actor user.User, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.Bookmark")
	defer span.End()
	if err := b.Authz.Can(ctx, "bookmark", "create", postID, 0); err != nil {
		return err
	}
	if _, err := b.readable(ctx, postID, actor.ID); err != nil {
		return err
	}
	return b.Social.Bookmark(ctx, actor.ID, postID)
}

func (b *Blog) Unbookmark(ctx context.Context, actor user.User, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.Unbookmark")
	defer span.End()
	return b.Social.Unbookmark(ctx, actor.ID, postID)
}

// Comments

func (b *Blog) ListComments(ctx context.Context, postID, viewerID int, p domain.Paging) (comment.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListComments")
	defer span.End()
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return comment.Page{}, err
	}
	return b.Comments.ListByPost(ctx, postID, viewerID, p)
}

// LikeComment likes a comment on a post the actor can see and notifies
// the comment's author once.
func (b *Blog) LikeComment(ctx context.Context, actor user.User, id int) (comment.Comment, error) {
	ctx, span := tracer.Start(ctx, "Blog.LikeComment")
	defer span.End()
	if err := b.Authz.Can(ctx, "like", "create", id, 0); err != nil {
		return comment.Comment{}, err
	}
	c, err := b.Comments.Get(ctx, id)
	if err != nil {
		return c, err
	}
	if _, err := b.readable(ctx, c.PostID, actor.ID); err != nil {
		return comment.Comment{}, err
	}
	c, created, err := b.Comments.Like(ctx, id, actor.ID)
	if err == nil && created {
		b.notifyLogged(ctx, social.Notification{Type: social.NotifyCommentLike, ActorID: actor.ID, PostID: c.PostID, CommentID: id}, c.UserID)
	}
	return c, err
}

func (b *Blog) UnlikeComment(ctx context.Context, actor user.User, id int) (comment.Comment, error) {
	ctx, span := tracer.Start(ctx, "Blog.UnlikeComment")
	defer span.End()
	return b.Comments.Unlike(ctx, id, actor.ID)
}

// AllComments is the moderation view.
func (b *Blog) AllComments(ctx context.Context, p domain.Paging) (comment.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.AllComments")
	defer span.End()
	if err := b.Authz.Can(ctx, "comment", "moderate", 0, 0); err != nil {
		return comment.Page{}, err
	}
	return b.Comments.ListAll(ctx, p)
}

func (b *Blog) AddComment(ctx context.Context, actor user.User, postID, parentID int, text string) (comment.Comment, error) {
	ctx, span := tracer.Start(ctx, "Blog.AddComment")
	defer span.End()
	if err := b.Authz.Can(ctx, "comment", "create", 0, 0); err != nil {
		return comment.Comment{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return comment.Comment{}, err
	}
	p, err := b.readable(ctx, postID, actor.ID)
	if err != nil {
		return comment.Comment{}, err
	}
	c, err := comment.New(postID, parentID, actor.ID, actor.Username, text)
	if err != nil {
		return c, err
	}
	if err := b.notBlocked(ctx, actor.ID, p.UserID); err != nil {
		return comment.Comment{}, err
	}
	if parentID != 0 {
		if parent, err := b.Comments.Get(ctx, parentID); err == nil {
			if err := b.notBlocked(ctx, actor.ID, parent.UserID); err != nil {
				return comment.Comment{}, err
			}
		}
	}
	if c, err = b.Comments.Add(ctx, c); err != nil {
		return c, err
	}
	b.mentionInComment(ctx, c, "")
	b.publishComment(ctx, "created", c)
	b.notifyLogged(ctx, social.Notification{Type: social.NotifyComment, ActorID: actor.ID, PostID: postID, CommentID: c.ID}, p.UserID)
	if parentID != 0 {
		if parent, err := b.Comments.Get(ctx, parentID); err == nil && parent.UserID != p.UserID {
			b.notifyLogged(ctx, social.Notification{Type: social.NotifyReply, ActorID: actor.ID, PostID: postID, CommentID: c.ID}, parent.UserID)
		}
	}
	return c, nil
}

// EditComment is allowed for the author (ABAC) and roles with comment.update.
func (b *Blog) EditComment(ctx context.Context, id int, text string) (comment.Comment, error) {
	ctx, span := tracer.Start(ctx, "Blog.EditComment")
	defer span.End()
	text, err := comment.ValidateText(text)
	if err != nil {
		return comment.Comment{}, err
	}
	c, err := b.Comments.Get(ctx, id)
	if err != nil {
		return c, err
	}
	if err := b.Authz.Can(ctx, "comment", "update", id, c.UserID); err != nil {
		return comment.Comment{}, err
	}
	old := c.Text
	if c, err = b.Comments.Edit(ctx, id, text); err == nil {
		b.publishComment(ctx, "updated", c)
		b.mentionInComment(ctx, c, old)
	}
	return c, err
}

// DeleteComment is allowed for the author (ABAC) and roles with comment.delete.
func (b *Blog) DeleteComment(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteComment")
	defer span.End()
	c, err := b.Comments.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := b.Authz.Can(ctx, "comment", "delete", id, c.UserID); err != nil {
		return err
	}
	if err := b.Comments.Delete(ctx, id); err != nil {
		return err
	}
	if actor.ID != c.UserID {
		b.Audit.Record(ctx, actor.ID, "comment.delete", "comment:"+itoa(id), map[string]any{"owner_id": c.UserID, "post_id": c.PostID})
	}
	b.publishComment(ctx, "deleted", c)
	return nil
}

func (b *Blog) publishComment(ctx context.Context, event string, c comment.Comment) {
	if err := b.Events.Publish(ctx, CommentsChannel(c.PostID), map[string]any{"event": event, "comment": c}); err != nil {
		zap.L().Warn("publish comment", zap.Any("ctx", ctx), zap.Error(err))
	}
}

// Categories and labels (writes need category.write / label.write)

func (b *Blog) ListCategories(ctx context.Context) ([]category.Category, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListCategories")
	defer span.End()
	return b.Categories.List(ctx)
}

func (b *Blog) CreateCategory(ctx context.Context, actor user.User, name string) (category.Category, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateCategory")
	defer span.End()
	if err := b.Authz.Can(ctx, "category", "write", 0, 0); err != nil {
		return category.Category{}, err
	}
	name, err := category.NormalizeName(name)
	if err != nil {
		return category.Category{}, err
	}
	c, err := b.Categories.Create(ctx, name)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "category.create", "category:"+itoa(c.ID), map[string]any{"name": name})
	}
	return c, err
}

func (b *Blog) DeleteCategory(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteCategory")
	defer span.End()
	if err := b.Authz.Can(ctx, "category", "write", id, 0); err != nil {
		return err
	}
	err := b.Categories.Delete(ctx, id)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "category.delete", "category:"+itoa(id), nil)
	}
	return err
}

func (b *Blog) CategoryPosts(ctx context.Context, id int, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.CategoryPosts")
	defer span.End()
	ok, err := b.Categories.Exists(ctx, id)
	if err != nil {
		return post.Page{}, err
	}
	if !ok {
		return post.Page{}, domain.ErrNotFound
	}
	return b.Posts.List(ctx, post.Filter{CategoryID: id, Status: post.StatusPublished, Paging: p})
}

func (b *Blog) Labels(ctx context.Context) ([]post.Label, error) {
	ctx, span := tracer.Start(ctx, "Blog.Labels")
	defer span.End()
	return b.Posts.Labels(ctx)
}

func (b *Blog) CreateLabel(ctx context.Context, actor user.User, name, color string) (post.Label, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateLabel")
	defer span.End()
	if err := b.Authz.Can(ctx, "label", "write", 0, 0); err != nil {
		return post.Label{}, err
	}
	l, err := post.NewLabel(name, color)
	if err != nil {
		return l, err
	}
	if l, err = b.Posts.CreateLabel(ctx, l); err == nil {
		b.Audit.Record(ctx, actor.ID, "label.create", "label:"+itoa(l.ID), map[string]any{"name": l.Name})
	}
	return l, err
}

func (b *Blog) DeleteLabel(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteLabel")
	defer span.End()
	if err := b.Authz.Can(ctx, "label", "write", id, 0); err != nil {
		return err
	}
	err := b.Posts.DeleteLabel(ctx, id)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "label.delete", "label:"+itoa(id), nil)
	}
	return err
}

// Notifications

// notify stores and pushes a notification to each recipient except the actor.
// notifyLogged notifies without failing the caller's action; errors are logged.
func (b *Blog) notifyLogged(ctx context.Context, n social.Notification, recipients ...int) {
	if err := b.notify(ctx, n, recipients...); err != nil {
		zap.L().Warn("notify", zap.Any("ctx", ctx), zap.String("type", string(n.Type)), zap.Error(err))
	}
}

func (b *Blog) notify(ctx context.Context, n social.Notification, recipients ...int) error {
	var to []int
	for _, id := range recipients {
		if id != 0 && id != n.ActorID {
			to = append(to, id)
		}
	}
	to = b.unsilenced(ctx, n.ActorID, to)
	if len(to) == 0 {
		return nil
	}
	sent, err := b.Social.Notify(ctx, n, to)
	if err != nil {
		zap.L().Error("notify", zap.Any("ctx", ctx), zap.Any("type", n.Type), zap.Error(err))
		return err
	}
	for _, s := range sent {
		if err := b.Events.Publish(ctx, NotificationsChannel(s.UserID), s); err != nil {
			zap.L().Warn("push notification", zap.Any("ctx", ctx), zap.Error(err))
		}
	}
	return nil
}

func (b *Blog) Notifications(ctx context.Context, actor user.User, p domain.Paging) (social.NotificationPage, error) {
	ctx, span := tracer.Start(ctx, "Blog.Notifications")
	defer span.End()
	return b.Social.Notifications(ctx, actor.ID, p)
}

// MarkNotificationsRead marks ids as read, or every notification when all
// is set. Empty ids without all is a no-op.
func (b *Blog) MarkNotificationsRead(ctx context.Context, actor user.User, ids []int64, all bool) error {
	ctx, span := tracer.Start(ctx, "Blog.MarkNotificationsRead")
	defer span.End()
	if all {
		return b.Social.MarkAllRead(ctx, actor.ID)
	}
	return b.Social.MarkRead(ctx, actor.ID, ids)
}

// Stats

func (b *Blog) Stats(ctx context.Context) (Stats, error) {
	ctx, span := tracer.Start(ctx, "Blog.Stats")
	defer span.End()
	if err := b.Authz.Can(ctx, "stats", "read", 0, 0); err != nil {
		return Stats{}, err
	}
	return b.Repos.Stats.Stats(ctx)
}

func ignoreNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}
