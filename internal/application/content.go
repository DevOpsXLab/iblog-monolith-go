package application

import (
	"context"
	"errors"
	"slices"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/comment"
	"github.com/iBlog/iblog-monolith-go/internal/domain/mention"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/relation"
	"github.com/iBlog/iblog-monolith-go/internal/domain/revision"
	"github.com/iBlog/iblog-monolith-go/internal/domain/series"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

// Blocks and mutes

func (b *Blog) Relate(ctx context.Context, actor user.User, username string, k relation.Kind) error {
	ctx, span := tracer.Start(ctx, "Blog.Relate")
	defer span.End()
	if !k.Valid() {
		return domain.Invalid("kind: block or mute")
	}
	target, err := b.Users.GetByUsername(ctx, username)
	if err != nil || target.DeletedAt != nil {
		return notFoundOr(err)
	}
	if target.ID == actor.ID {
		return domain.Invalid("cannot " + string(k) + " yourself")
	}
	return b.Relations.Add(ctx, actor.ID, target.ID, k)
}

func (b *Blog) Unrelate(ctx context.Context, actor user.User, username string, k relation.Kind) error {
	ctx, span := tracer.Start(ctx, "Blog.Unrelate")
	defer span.End()
	target, err := b.Users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	return b.Relations.Remove(ctx, actor.ID, target.ID, k)
}

func (b *Blog) Related(ctx context.Context, actor user.User, k relation.Kind, p domain.Paging) (user.SummaryPage, error) {
	ctx, span := tracer.Start(ctx, "Blog.Related")
	defer span.End()
	return b.Relations.List(ctx, actor.ID, k, p)
}

// notBlocked fails when either user blocked the other.
func (b *Blog) notBlocked(ctx context.Context, a, other int) error {
	if b.Relations == nil || other == 0 || a == other {
		return nil
	}
	blocked, err := b.Relations.Blocked(ctx, a, other)
	if err == nil && blocked {
		err = relation.ErrBlocked
	}
	return err
}

// unsilenced drops recipients who blocked or muted the actor.
func (b *Blog) unsilenced(ctx context.Context, actorID int, to []int) []int {
	if b.Relations == nil || actorID == 0 || len(to) == 0 {
		return to
	}
	silent, err := b.Relations.Silenced(ctx, actorID, to)
	if err != nil {
		zap.L().Warn("silenced", zap.Any("ctx", ctx), zap.Error(err)) // notify anyway
		return to
	}
	return slices.DeleteFunc(to, func(id int) bool { return slices.Contains(silent, id) })
}

// Mentions

// mentionInPost notifies users newly @mentioned in a public post's body.
func (b *Blog) mentionInPost(ctx context.Context, p post.Post, oldBody string) {
	b.mention(ctx, mention.New(oldBody, p.Body), social.Notification{
		Type: social.NotifyMention, ActorID: p.UserID, PostID: p.ID,
	})
}

// mentionInComment notifies users newly @mentioned in a comment on a
// public post; the post author already gets a comment notification.
func (b *Blog) mentionInComment(ctx context.Context, c comment.Comment, oldText string) {
	names := mention.New(oldText, c.Text)
	if len(names) == 0 {
		return
	}
	p, err := b.Posts.Get(ctx, c.PostID, 0)
	if err != nil || !p.IsPublic() {
		return
	}
	b.mention(ctx, names, social.Notification{
		Type: social.NotifyMention, ActorID: c.UserID, PostID: c.PostID, CommentID: c.ID,
	}, p.UserID)
}

func (b *Blog) mention(ctx context.Context, names []string, n social.Notification, skip ...int) {
	if b.Discovery == nil || len(names) == 0 || n.ActorID == 0 {
		return
	}
	ids, err := b.Discovery.UserIDs(ctx, names)
	if err != nil {
		zap.L().Error("mentions", zap.Any("ctx", ctx), zap.Error(err))
		return
	}
	ids = slices.DeleteFunc(ids, func(id int) bool { return slices.Contains(skip, id) })
	var to []int
	for _, id := range ids {
		if b.notBlocked(ctx, n.ActorID, id) == nil {
			to = append(to, id)
		}
	}
	b.notify(ctx, n, to...)
}

// Revisions

// revisionUpdater is implemented by post repositories that can update a
// post and record its previous text as a revision in one transaction.
type revisionUpdater interface {
	UpdateWithRevision(ctx context.Context, id, editorID int, d post.Draft) (post.Post, error)
}

// updateWithRevision updates the post and keeps the replaced text as a
// revision. Atomic when the repository supports it; otherwise update then
// snapshot, surfacing a failed snapshot to the caller.
func (b *Blog) updateWithRevision(ctx context.Context, actor user.User, before post.Post, d post.Draft) (post.Post, error) {
	if b.Revisions != nil {
		if u, ok := b.Posts.(revisionUpdater); ok {
			return u.UpdateWithRevision(ctx, before.ID, actor.ID, d)
		}
	}
	p, err := b.Posts.Update(ctx, before.ID, d)
	if err != nil {
		return p, err
	}
	return p, b.snapshot(ctx, actor, before, p)
}

// snapshot keeps the text before an edit as a revision.
func (b *Blog) snapshot(ctx context.Context, actor user.User, before, after post.Post) error {
	if b.Revisions == nil {
		return nil
	}
	old := revision.Content{Title: before.Title, Subtitle: before.Subtitle, Body: before.Body}
	if old.Equal(revision.Content{Title: after.Title, Subtitle: after.Subtitle, Body: after.Body}) {
		return nil
	}
	_, err := b.Revisions.Add(ctx, before.ID, actor.ID, old)
	return err
}

// canEdit is UpdatePost's permission check.
func (b *Blog) canEdit(ctx context.Context, actor user.User, id int) (post.Post, error) {
	p, err := b.Posts.Get(ctx, id, 0)
	if err != nil {
		return p, err
	}
	if err := b.Authz.Can(ctx, "post", "update", id, p.UserID); err != nil {
		if !errors.Is(err, domain.ErrForbidden) || !b.isPublicationEditor(ctx, actor, p.PublicationID) {
			return post.Post{}, err
		}
	}
	return p, nil
}

// Revisions lists a post's earlier versions (for those who may edit it).
func (b *Blog) PostRevisions(ctx context.Context, actor user.User, postID int) ([]revision.Revision, error) {
	ctx, span := tracer.Start(ctx, "Blog.PostRevisions")
	defer span.End()
	if _, err := b.canEdit(ctx, actor, postID); err != nil {
		return nil, err
	}
	list, err := b.Revisions.List(ctx, postID)
	if list == nil {
		list = []revision.Revision{}
	}
	return list, err
}

// RevisionDetail is a revision with a diff against the current text.
type RevisionDetail struct {
	revision.Revision
	Diff revision.Diff `json:"diff"`
}

func (b *Blog) PostRevision(ctx context.Context, actor user.User, postID, version int) (RevisionDetail, error) {
	ctx, span := tracer.Start(ctx, "Blog.PostRevision")
	defer span.End()
	p, err := b.canEdit(ctx, actor, postID)
	if err != nil {
		return RevisionDetail{}, err
	}
	v, err := b.Revisions.Get(ctx, postID, version)
	if err != nil {
		return RevisionDetail{}, err
	}
	diff := revision.Compare(revision.Content{Title: v.Title, Subtitle: v.Subtitle, Body: v.Body},
		revision.Content{Title: p.Title, Subtitle: p.Subtitle, Body: p.Body})
	return RevisionDetail{Revision: v, Diff: diff}, nil
}

// RestoreRevision makes an earlier version current; the current text
// becomes a new revision, so a restore can be undone.
func (b *Blog) RestoreRevision(ctx context.Context, actor user.User, postID, version int) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.RestoreRevision")
	defer span.End()
	p, err := b.canEdit(ctx, actor, postID)
	if err != nil {
		return post.Post{}, err
	}
	v, err := b.Revisions.Get(ctx, postID, version)
	if err != nil {
		return post.Post{}, err
	}
	d := post.Draft{
		Title: v.Title, Subtitle: v.Subtitle, Body: v.Body,
		Status: p.Status, PublishAt: p.PublishAt, CategoryID: p.CategoryID, PublicationID: p.PublicationID,
		CoverURL: p.CoverURL, CanonicalURL: p.CanonicalURL, Tags: p.Tags,
	}
	for _, l := range p.Labels {
		d.LabelIDs = append(d.LabelIDs, l.ID)
	}
	out, err := b.UpdatePost(ctx, actor, postID, d)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "post.restore_revision", "post:"+itoa(postID), map[string]any{"version": version})
	}
	return out, err
}

// Series

func (b *Blog) CreateSeries(ctx context.Context, actor user.User, in series.SeriesInput) (series.Series, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateSeries")
	defer span.End()
	if err := b.Authz.Can(ctx, "post", "create", 0, 0); err != nil {
		return series.Series{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return series.Series{}, err
	}
	in, err := in.Normalize()
	if err != nil {
		return series.Series{}, err
	}
	return b.Series.Create(ctx, actor.ID, post.NewSlug(in.Title), in)
}

// ownSeries loads a series the actor may change: its author, or roles
// with post.update.
func (b *Blog) ownSeries(ctx context.Context, slug string) (series.Series, error) {
	s, err := b.Series.GetBySlug(ctx, slug)
	if err != nil {
		return s, err
	}
	return s, b.Authz.Can(ctx, "post", "update", 0, s.UserID)
}

func (b *Blog) UpdateSeries(ctx context.Context, slug string, in series.SeriesInput) (series.Series, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdateSeries")
	defer span.End()
	in, err := in.Normalize()
	if err != nil {
		return series.Series{}, err
	}
	s, err := b.ownSeries(ctx, slug)
	if err != nil {
		return s, err
	}
	return b.Series.Update(ctx, s.ID, in)
}

// DeleteSeries removes the series; its posts stay.
func (b *Blog) DeleteSeries(ctx context.Context, slug string) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteSeries")
	defer span.End()
	s, err := b.ownSeries(ctx, slug)
	if err != nil {
		return err
	}
	return b.Series.Delete(ctx, s.ID)
}

// SetSeriesPosts replaces the parts with the author's posts in this order.
func (b *Blog) SetSeriesPosts(ctx context.Context, slug string, postIDs []int) (series.Detail, error) {
	ctx, span := tracer.Start(ctx, "Blog.SetSeriesPosts")
	defer span.End()
	if err := series.ValidateOrder(postIDs); err != nil {
		return series.Detail{}, err
	}
	s, err := b.ownSeries(ctx, slug)
	if err != nil {
		return series.Detail{}, err
	}
	if err := b.Series.SetPosts(ctx, s.ID, s.UserID, postIDs); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return series.Detail{}, domain.Invalid("post_ids must be the series author's posts")
		}
		return series.Detail{}, err
	}
	return b.seriesDetail(ctx, s.Slug, true)
}

// GetSeries shows published parts; the author also sees drafts.
func (b *Blog) GetSeries(ctx context.Context, slug string, viewerID int) (series.Detail, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetSeries")
	defer span.End()
	return b.seriesDetail(ctx, slug, viewerID != 0 && b.Authz.Can(ctx, "post", "read_draft", 0, b.seriesOwner(ctx, slug)) == nil)
}

func (b *Blog) seriesOwner(ctx context.Context, slug string) int {
	s, err := b.Series.GetBySlug(ctx, slug)
	if err != nil {
		return 0
	}
	return s.UserID
}

func (b *Blog) seriesDetail(ctx context.Context, slug string, all bool) (series.Detail, error) {
	s, err := b.Series.GetBySlug(ctx, slug)
	if err != nil {
		return series.Detail{}, err
	}
	parts, err := b.Series.Parts(ctx, s.ID, all)
	if parts == nil {
		parts = []series.Part{}
	}
	return series.Detail{Series: s, Posts: parts}, err
}

func (b *Blog) UserSeries(ctx context.Context, username string) ([]series.Series, error) {
	ctx, span := tracer.Start(ctx, "Blog.UserSeries")
	defer span.End()
	u, err := b.Users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		return nil, notFoundOr(err)
	}
	list, err := b.Series.ByUser(ctx, u.ID)
	if list == nil {
		list = []series.Series{}
	}
	return list, err
}

// PostSeries is a readable post's place in its series (published parts).
func (b *Blog) PostSeries(ctx context.Context, postID, viewerID int) (series.Navigation, error) {
	ctx, span := tracer.Start(ctx, "Blog.PostSeries")
	defer span.End()
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return series.Navigation{}, err
	}
	s, err := b.Series.OfPost(ctx, postID)
	if err != nil {
		return series.Navigation{}, err
	}
	parts, err := b.Series.Parts(ctx, s.ID, false)
	if err != nil {
		return series.Navigation{}, err
	}
	n, ok := series.Navigate(s, parts, postID)
	if !ok {
		return series.Navigation{}, domain.ErrNotFound // the post is not published
	}
	return n, nil
}

// Recommendations

// RelatedPosts are published posts sharing tags or the category.
func (b *Blog) RelatedPosts(ctx context.Context, postID, viewerID, limit int) ([]post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.RelatedPosts")
	defer span.End()
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return nil, err
	}
	ids, err := b.Discovery.RelatedPosts(ctx, postID, min(max(limit, 1), 20))
	if err != nil || len(ids) == 0 {
		return []post.Post{}, err
	}
	return b.Posts.ListByIDs(ctx, ids)
}

// SuggestUsers is who to follow for the actor.
func (b *Blog) SuggestUsers(ctx context.Context, actor user.User, limit int) ([]SuggestedUser, error) {
	ctx, span := tracer.Start(ctx, "Blog.SuggestUsers")
	defer span.End()
	list, err := b.Discovery.SuggestUsers(ctx, actor.ID, min(max(limit, 1), 50))
	if list == nil {
		list = []SuggestedUser{}
	}
	return list, err
}
