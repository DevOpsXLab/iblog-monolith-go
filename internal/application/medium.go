package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/readinglist"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

// Reads and per-day stats

// ReadPost records that a reader got through a post (see post.IsRead) and
// reports whether it counted. Authors reading their own post do not count.
func (b *Blog) ReadPost(ctx context.Context, id, viewerID int, reader string, progress float64, seconds int) (bool, error) {
	ctx, span := tracer.Start(ctx, "Blog.ReadPost")
	defer span.End()
	if err := post.ValidateRead(progress, seconds); err != nil {
		return false, err
	}
	p, err := b.Posts.Get(ctx, id, 0)
	if err != nil {
		return false, err
	}
	if !p.IsReadable() {
		return false, domain.ErrNotFound
	}
	if viewerID != 0 && viewerID == p.UserID || !post.IsRead(progress, seconds, p.ReadingTime) {
		return false, nil
	}
	return b.Posts.Read(ctx, id, viewerID, reader)
}

// PostDailyStats is a post's views and reads per day, oldest first.
type PostDailyStats struct {
	PostID int              `json:"post_id"`
	Days   []post.DailyStat `json:"days"`
}

const maxStatDays = 30

// DailyStats is for whoever may edit the post.
func (b *Blog) DailyStats(ctx context.Context, actor user.User, postID, days int) (PostDailyStats, error) {
	ctx, span := tracer.Start(ctx, "Blog.DailyStats")
	defer span.End()
	if _, err := b.canEdit(ctx, actor, postID); err != nil {
		return PostDailyStats{}, err
	}
	days = min(max(days, 1), maxStatDays)
	today := b.now().UTC().Truncate(24 * time.Hour)
	dates := make([]time.Time, days)
	for i := range dates {
		dates[i] = today.AddDate(0, 0, i-days+1)
	}
	views, err := b.Views.Daily(ctx, postID, dates)
	if err != nil {
		return PostDailyStats{}, err
	}
	reads, err := b.Posts.DailyReads(ctx, postID, dates[0])
	if err != nil {
		return PostDailyStats{}, err
	}
	out := PostDailyStats{PostID: postID, Days: make([]post.DailyStat, days)}
	for i, d := range dates {
		key := d.Format("2006-01-02")
		out.Days[i] = post.DailyStat{Date: key, Views: views[i], Reads: reads[key]}
	}
	return out, nil
}

// For You

// ForYou is the actor's ranked home feed (see Discovery.ForYou).
func (b *Blog) ForYou(ctx context.Context, actor user.User, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ForYou")
	defer span.End()
	page := post.Page{Items: []post.Post{}, Page: p.Page, Limit: p.Limit}
	ids, total, err := b.Discovery.ForYou(ctx, actor.ID, p.Limit, p.Offset())
	if err != nil || len(ids) == 0 {
		return page, err
	}
	page.Total = total
	page.Items, err = b.Posts.ListByIDs(ctx, ids)
	return page, err
}

// Show less like this

// Hide keeps a post, an author (username) or a tag out of the actor's feeds.
func (b *Blog) Hide(ctx context.Context, actor user.User, k social.HideKind, target string) error {
	ctx, span := tracer.Start(ctx, "Blog.Hide")
	defer span.End()
	key, err := b.hideKey(ctx, k, target)
	if err != nil {
		return err
	}
	if n, err := b.Social.CountHidden(ctx, actor.ID); err != nil {
		return err
	} else if n >= maxHides {
		return domain.Invalid("too many hidden items; remove some first")
	}
	return b.Social.Hide(ctx, actor.ID, k, key)
}

func (b *Blog) Unhide(ctx context.Context, actor user.User, k social.HideKind, target string) error {
	ctx, span := tracer.Start(ctx, "Blog.Unhide")
	defer span.End()
	key, err := b.hideKey(ctx, k, target)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return b.Social.Unhide(ctx, actor.ID, k, key)
}

func (b *Blog) Hidden(ctx context.Context, actor user.User, p domain.Paging) (social.HidePage, error) {
	ctx, span := tracer.Start(ctx, "Blog.Hidden")
	defer span.End()
	return b.Social.Hidden(ctx, actor.ID, p)
}

const maxHides = 1000

// hideKey is what feed_hides stores: post id, author user id or tag.
func (b *Blog) hideKey(ctx context.Context, k social.HideKind, target string) (string, error) {
	switch k {
	case social.HidePost:
		id, err := strconv.Atoi(target)
		if err != nil || id <= 0 {
			return "", domain.Invalid("target: post id")
		}
		if _, err := b.Posts.Get(ctx, id, 0); err != nil {
			return "", err
		}
		return target, nil
	case social.HideAuthor:
		u, err := b.Users.GetByUsername(ctx, target)
		if err != nil {
			return "", err
		}
		return itoa(u.ID), nil
	case social.HideTag:
		return post.ValidateTag(target)
	}
	return "", domain.Invalid("kind: post, author or tag")
}

// Pinned story

// Pin shows one of the actor's published posts first on their profile;
// postID 0 unpins.
func (b *Blog) Pin(ctx context.Context, actor user.User, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.Pin")
	defer span.End()
	if postID != 0 {
		p, err := b.Posts.Get(ctx, postID, 0)
		if err != nil {
			return err
		}
		if p.UserID != actor.ID || !p.IsPublic() {
			return domain.Invalid("pin one of your published posts")
		}
	}
	return b.Users.SetPinned(ctx, actor.ID, postID)
}

// Email subscriptions

func (b *Blog) Subscribe(ctx context.Context, actor user.User, username string) error {
	ctx, span := tracer.Start(ctx, "Blog.Subscribe")
	defer span.End()
	if !actor.EmailVerified {
		return domain.ErrEmailNotVerified
	}
	target, err := b.Users.GetByUsername(ctx, username)
	if err != nil || target.DeletedAt != nil {
		return notFoundOr(err)
	}
	if target.ID == actor.ID {
		return domain.Invalid("cannot subscribe to yourself")
	}
	if err := b.notBlocked(ctx, actor.ID, target.ID); err != nil {
		return err
	}
	return b.Users.Subscribe(ctx, actor.ID, target.ID)
}

func (b *Blog) Unsubscribe(ctx context.Context, actor user.User, username string) error {
	ctx, span := tracer.Start(ctx, "Blog.Unsubscribe")
	defer span.End()
	target, err := b.Users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	return b.Users.Unsubscribe(ctx, actor.ID, target.ID)
}

// emailSubscribers emails a new public post to its author's subscribers,
// except those who blocked or muted the author.
func (b *Blog) emailSubscribers(ctx context.Context, p post.Post) error {
	subs, err := b.Users.Subscribers(ctx, p.UserID)
	if err != nil || len(subs) == 0 {
		return err
	}
	ids := make([]int, len(subs))
	for i, s := range subs {
		ids[i] = s.ID
	}
	keep := map[int]bool{}
	for _, id := range b.unsilenced(ctx, p.UserID, ids) {
		keep[id] = true
	}
	link := b.SiteURL + "/p/" + p.Slug // the client route that resolves by slug alone
	text := p.Title
	if p.Subtitle != "" {
		text += "\n" + p.Subtitle
	}
	var errs []error
	for _, s := range subs {
		if !keep[s.ID] {
			continue
		}
		unsub := b.unsubscribeURL(s.ID, p.UserID)
		body := fmt.Sprintf("%s published a new story:\n\n%s\n\nRead it: %s\n\nStop these emails: %s/@%s",
			p.Author, text, link, b.SiteURL, p.Author)
		errs = append(errs, b.Jobs.SendEmailOnce(ctx, fmt.Sprintf("new-post:%d:%d", p.ID, s.ID),
			Email{To: s.Email, Subject: "New from " + p.Author + ": " + p.Title, Text: body, Unsubscribe: unsub}))
	}
	return errors.Join(errs...)
}

// unsubscribeSig signs (subscriber, author) for one-click unsubscribe.
func (b *Blog) unsubscribeSig(subscriberID, authorID int) string {
	mac := hmac.New(sha256.New, b.UnsubscribeKey)
	fmt.Fprintf(mac, "unsubscribe:%d:%d", subscriberID, authorID)
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func (b *Blog) unsubscribeURL(subscriberID, authorID int) string {
	if b.APIURL == "" || len(b.UnsubscribeKey) == 0 {
		return ""
	}
	return fmt.Sprintf("%s/api/unsubscribe?s=%d&a=%d&sig=%s", b.APIURL, subscriberID, authorID,
		b.unsubscribeSig(subscriberID, authorID))
}

// UnsubscribeByLink ends a subscription from a signed email link, without
// a session (one-click unsubscribe).
func (b *Blog) UnsubscribeByLink(ctx context.Context, subscriberID, authorID int, sig string) error {
	ctx, span := tracer.Start(ctx, "Blog.UnsubscribeByLink")
	defer span.End()
	if len(b.UnsubscribeKey) == 0 || !hmac.Equal([]byte(sig), []byte(b.unsubscribeSig(subscriberID, authorID))) {
		return domain.Invalid("bad unsubscribe link")
	}
	return b.Users.Unsubscribe(ctx, subscriberID, authorID)
}

// Reading lists

func (b *Blog) CreateList(ctx context.Context, actor user.User, in readinglist.ListInput) (readinglist.List, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateList")
	defer span.End()
	in, err := in.Normalize()
	if err != nil {
		return readinglist.List{}, err
	}
	n, err := b.Lists.Count(ctx, actor.ID)
	if err != nil {
		return readinglist.List{}, err
	}
	if n >= readinglist.MaxLists {
		return readinglist.List{}, readinglist.ErrTooMany
	}
	return b.Lists.Create(ctx, actor.ID, post.NewSlug(in.Name), in)
}

// list loads a list the viewer may see: public, or their own.
func (b *Blog) list(ctx context.Context, slug string, viewerID int) (readinglist.List, error) {
	l, err := b.Lists.GetBySlug(ctx, slug)
	if err == nil && l.Private && l.UserID != viewerID {
		return readinglist.List{}, domain.ErrNotFound
	}
	return l, err
}

// ownList loads a list the actor owns; others' lists are 404 (private) or 403.
func (b *Blog) ownList(ctx context.Context, actor user.User, slug string) (readinglist.List, error) {
	l, err := b.list(ctx, slug, actor.ID)
	if err == nil && l.UserID != actor.ID {
		return readinglist.List{}, domain.ErrForbidden
	}
	return l, err
}

func (b *Blog) GetList(ctx context.Context, slug string, viewerID int) (readinglist.List, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetList")
	defer span.End()
	return b.list(ctx, slug, viewerID)
}

func (b *Blog) UpdateList(ctx context.Context, actor user.User, slug string, in readinglist.ListInput) (readinglist.List, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdateList")
	defer span.End()
	in, err := in.Normalize()
	if err != nil {
		return readinglist.List{}, err
	}
	l, err := b.ownList(ctx, actor, slug)
	if err != nil {
		return l, err
	}
	return b.Lists.Update(ctx, l.ID, in)
}

func (b *Blog) DeleteList(ctx context.Context, actor user.User, slug string) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteList")
	defer span.End()
	l, err := b.ownList(ctx, actor, slug)
	if err != nil {
		return err
	}
	return b.Lists.Delete(ctx, l.ID)
}

// ListItems pages the published posts in a list, newest first.
func (b *Blog) ListItems(ctx context.Context, slug string, viewerID int, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListItems")
	defer span.End()
	l, err := b.list(ctx, slug, viewerID)
	if err != nil {
		return post.Page{}, err
	}
	return b.Posts.List(ctx, post.Filter{InList: l.ID, Status: post.StatusPublished, Paging: p})
}

// AddToList saves a post the actor can read.
func (b *Blog) AddToList(ctx context.Context, actor user.User, slug string, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.AddToList")
	defer span.End()
	l, err := b.ownList(ctx, actor, slug)
	if err != nil {
		return err
	}
	if _, err := b.readable(ctx, postID, actor.ID); err != nil {
		return err
	}
	return b.Lists.Add(ctx, l.ID, postID)
}

func (b *Blog) RemoveFromList(ctx context.Context, actor user.User, slug string, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.RemoveFromList")
	defer span.End()
	l, err := b.ownList(ctx, actor, slug)
	if err != nil {
		return err
	}
	return b.Lists.Remove(ctx, l.ID, postID)
}

// MyLists are all the actor's lists; postID > 0 fills contains.
func (b *Blog) MyLists(ctx context.Context, actor user.User, postID int, p domain.Paging) (readinglist.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyLists")
	defer span.End()
	return b.Lists.ByUser(ctx, actor.ID, true, postID, p)
}

// UserLists are a user's public lists (all of them for the user).
func (b *Blog) UserLists(ctx context.Context, username string, viewerID int, p domain.Paging) (readinglist.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.UserLists")
	defer span.End()
	u, err := b.Users.GetByUsername(ctx, username)
	if err != nil || u.DeletedAt != nil {
		return readinglist.Page{}, notFoundOr(err)
	}
	return b.Lists.ByUser(ctx, u.ID, u.ID == viewerID, 0, p)
}

// Import

// ImportPost fetches a story from another site as a draft of the actor's,
// with canonical_url pointing back to it.
func (b *Blog) ImportPost(ctx context.Context, actor user.User, url string) (post.Post, error) {
	ctx, span := tracer.Start(ctx, "Blog.ImportPost")
	defer span.End()
	if b.Importer == nil {
		return post.Post{}, domain.Invalid("import is not available")
	}
	if err := b.Authz.Can(ctx, "post", "create", 0, 0); err != nil {
		return post.Post{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return post.Post{}, err
	}
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") || len(url) > 2000 {
		return post.Post{}, domain.Invalid("url: http(s) address")
	}
	in, err := b.Importer.Fetch(ctx, url)
	if err != nil {
		zap.L().Info("import", zap.Any("ctx", ctx), zap.String("url", url), zap.Error(err))
		var invalid *domain.ValidationError
		if errors.As(err, &invalid) {
			return post.Post{}, err
		}
		return post.Post{}, domain.Invalid("could not import that page")
	}
	d := post.Draft{
		Title: truncate(in.Title, 200), Subtitle: truncate(in.Subtitle, 300), Body: in.Body, Status: post.StatusDraft,
		CoverURL: in.CoverURL, CanonicalURL: in.CanonicalURL,
	}
	if d.Title == "" {
		d.Title = "Imported story"
	}
	return b.CreatePost(ctx, actor, d)
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n])
}

func utf8RuneStart(c byte) bool { return c&0xC0 != 0x80 }
