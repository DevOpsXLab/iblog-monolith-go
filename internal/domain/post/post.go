package post

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type Status string

const (
	StatusDraft     Status = "draft"
	StatusScheduled Status = "scheduled"
	StatusPublished Status = "published"
	// StatusUnlisted posts are readable by link but left out of listings,
	// feeds, search and recommendations.
	StatusUnlisted Status = "unlisted"
)

// Post is the aggregate root. CategoryID/UserID 0 mean "none".
// Body is Markdown; clients render it.
type Post struct {
	ID            int        `json:"id"`
	Title         string     `json:"title"`
	Subtitle      string     `json:"subtitle"`
	Slug          string     `json:"slug"`
	Status        Status     `json:"status"`
	Body          string     `json:"body"`
	BodyHTML      string     `json:"body_html,omitempty"` // sanitized; single-post reads only
	ReadingTime   int        `json:"reading_time"`        // minutes
	PublishAt     *time.Time `json:"publish_at,omitempty"`
	PublishedAt   *time.Time `json:"published_at"`
	Author        string     `json:"author"`
	CategoryID    int        `json:"category_id"`
	PublicationID int        `json:"publication_id"` // 0 = personal
	UserID        int        `json:"user_id"`
	CoverURL      string     `json:"cover_url"`
	CanonicalURL  string     `json:"canonical_url"` // where the story first appeared
	Tags          []string   `json:"tags"`
	Labels        []Label    `json:"labels"`
	Likes         int        `json:"likes"` // number of clappers
	Claps         int        `json:"claps"` // total claps
	CommentsCount int        `json:"comments_count"`
	Views         int64      `json:"views"`
	Liked         bool       `json:"liked"`      // by the current viewer
	MyClaps       int        `json:"my_claps"`   // by the current viewer
	Bookmarked    bool       `json:"bookmarked"` // by the current viewer
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// IsPublic: listed, announced to followers and searchable.
func (p Post) IsPublic() bool { return p.Status == StatusPublished }

// IsReadable: anyone with the link may read it (published or unlisted).
func (p Post) IsReadable() bool { return p.Status == StatusPublished || p.Status == StatusUnlisted }

// Draft is the editable part of the post, the base an update is decoded
// onto so fields the client leaves out keep their values.
func (p Post) Draft() Draft {
	d := Draft{
		Title: p.Title, Subtitle: p.Subtitle, Status: p.Status, PublishAt: p.PublishAt,
		Body: p.Body, CategoryID: p.CategoryID, PublicationID: p.PublicationID,
		CoverURL: p.CoverURL, CanonicalURL: p.CanonicalURL, Tags: p.Tags,
	}
	for _, l := range p.Labels {
		d.LabelIDs = append(d.LabelIDs, l.ID)
	}
	return d
}

// IsRead tells whether a reader got through a post: most of it scrolled
// or about half its reading time spent on it.
func IsRead(progress float64, seconds, readingTime int) bool {
	return progress >= 0.6 || seconds >= max(readingTime, 1)*30
}

// ValidateRead checks a read report.
func ValidateRead(progress float64, seconds int) error {
	if progress < 0 || progress > 1 || seconds < 0 || seconds > 24*3600 {
		return domain.Invalid("progress: 0-1, seconds: 0-86400")
	}
	return nil
}

// Label is a curated, coloured marker managed by admins (tags are free-form).
type Label struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// NewLabel validates a label; color defaults to grey.
func NewLabel(name, color string) (Label, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return Label{}, domain.Invalid("name required")
	case len(name) > 30:
		return Label{}, domain.Invalid("name too long")
	}
	if color == "" {
		color = "#6b7280"
	}
	if !colorRe.MatchString(color) {
		return Label{}, domain.Invalid("color must be #rrggbb")
	}
	return Label{Name: name, Color: strings.ToLower(color)}, nil
}

// Draft is the editable part of a post. Empty Status means published.
type Draft struct {
	Title         string     `json:"title"`
	Subtitle      string     `json:"subtitle"`
	Status        Status     `json:"status"`
	PublishAt     *time.Time `json:"publish_at"` // required when status is scheduled
	Body          string     `json:"body"`
	CategoryID    int        `json:"category_id"`
	PublicationID int        `json:"publication_id"` // 0 = personal
	CoverURL      string     `json:"cover_url"`
	CanonicalURL  string     `json:"canonical_url"`
	Tags          []string   `json:"tags"`
	LabelIDs      []int      `json:"label_ids"`
}

const (
	maxTitle  = 200
	maxSub    = 300
	maxBody   = 100_000
	maxTags   = 10
	maxLabels = 5
)

// Normalize validates d and cleans it up; now checks publish_at.
func (d Draft) Normalize(now time.Time) (Draft, error) {
	d.Title = strings.TrimSpace(d.Title)
	switch {
	case d.Title == "":
		return d, domain.Invalid("title required")
	case len(d.Title) > maxTitle:
		return d, domain.Invalid("title too long")
	case len(d.Subtitle) > maxSub:
		return d, domain.Invalid("subtitle too long")
	case len(d.Body) > maxBody:
		return d, domain.Invalid("body too long")
	case d.CategoryID < 0:
		return d, domain.Invalid("bad category_id")
	case d.PublicationID < 0:
		return d, domain.Invalid("bad publication_id")
	}
	d.Subtitle = strings.TrimSpace(d.Subtitle)
	switch d.Status {
	case "":
		d.Status = StatusPublished
		d.PublishAt = nil
	case StatusDraft, StatusPublished, StatusUnlisted:
		d.PublishAt = nil
	case StatusScheduled:
		if d.PublishAt == nil || !d.PublishAt.After(now) {
			return d, domain.Invalid("scheduled posts need a future publish_at")
		}
	default:
		return d, domain.Invalid("status: draft, scheduled, published or unlisted")
	}
	d.CoverURL = strings.TrimSpace(d.CoverURL)
	if d.CoverURL != "" && !strings.HasPrefix(d.CoverURL, "/api/uploads/") &&
		!strings.HasPrefix(d.CoverURL, "https://") && !strings.HasPrefix(d.CoverURL, "http://") {
		return d, domain.Invalid("bad cover_url")
	}
	d.CanonicalURL = strings.TrimSpace(d.CanonicalURL)
	if d.CanonicalURL != "" && (len(d.CanonicalURL) > 2000 ||
		!strings.HasPrefix(d.CanonicalURL, "https://") && !strings.HasPrefix(d.CanonicalURL, "http://")) {
		return d, domain.Invalid("bad canonical_url")
	}
	d.Tags = normalizeTags(d.Tags)
	if len(d.Tags) > maxTags {
		return d, domain.Invalid("too many tags")
	}
	d.LabelIDs = slices.Compact(slices.Sorted(slices.Values(d.LabelIDs)))
	if len(d.LabelIDs) > maxLabels {
		return d, domain.Invalid("too many labels")
	}
	return d, nil
}

// ReadingTime estimates minutes to read body at 200 words per minute.
func ReadingTime(body string) int {
	return max(1, (len(strings.Fields(body))+199)/200)
}

// NewSlug builds a URL slug from title plus a random suffix, so equal
// titles never collide.
func NewSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	base := strings.TrimRight(b.String(), "-")
	if base == "" {
		base = "post"
	}
	suffix := make([]byte, 4)
	rand.Read(suffix)
	return base + "-" + hex.EncodeToString(suffix)
}

// NormalizeTag lower-cases and trims a tag.
func NormalizeTag(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

// ValidateTag normalizes a single tag given on its own (e.g. to follow).
func ValidateTag(t string) (string, error) {
	t = NormalizeTag(t)
	if t == "" || len(t) > 40 {
		return t, domain.Invalid("tag: 1-40 chars")
	}
	return t, nil
}

func normalizeTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = NormalizeTag(t)
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

type Filter struct {
	Query      string
	Tag        string
	CategoryID int
	LabelID    int
	UserID     int
	// PublicationID lists posts in one publication.
	PublicationID int
	// FeedOf lists posts by authors and with tags this user follows.
	FeedOf int
	// BookmarkedBy lists posts this user bookmarked.
	BookmarkedBy int
	// InList lists posts saved to a reading list.
	InList int
	// HideFor leaves out posts, authors and tags this user hid.
	HideFor int
	// Status filters by status; empty means published.
	Status Status
	domain.Paging
}

// Page is one page of posts.
type Page struct {
	Items []Post `json:"items"`
	Total int    `json:"total"` // with a cursor: items from the cursor on
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
	// NextCursor fetches the next page with ?cursor=; empty on the last page
	// and for search results (ranked, so offset-paged only).
	NextCursor string `json:"next_cursor,omitempty"`
}

// Cursor is a keyset position: posts sort by (SortTime, ID) descending.
type Cursor struct {
	At time.Time
	ID int
}

// SortTime is the time posts are ordered by: published, else scheduled,
// else last edited.
func (p Post) SortTime() time.Time {
	switch {
	case p.PublishedAt != nil:
		return *p.PublishedAt
	case p.PublishAt != nil:
		return *p.PublishAt
	}
	return p.UpdatedAt
}

// CursorAfter is the cursor that continues after p.
func CursorAfter(p Post) string {
	raw := strconv.FormatInt(p.SortTime().UnixMicro(), 10) + ":" + strconv.Itoa(p.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var ErrBadCursor = domain.Invalid("bad cursor")

// ParseCursor decodes a CursorAfter value.
func ParseCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	at, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return Cursor{}, ErrBadCursor
	}
	micros, err1 := strconv.ParseInt(at, 10, 64)
	n, err2 := strconv.Atoi(id)
	if err1 != nil || err2 != nil || n <= 0 {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{At: time.UnixMicro(micros).UTC(), ID: n}, nil
}

// MaxClaps is how many claps one reader may give one post.
const MaxClaps = 50

// ValidateClaps checks how many claps one request adds.
func ValidateClaps(n int) error {
	if n < 1 || n > MaxClaps {
		return domain.Invalid("count: 1-50")
	}
	return nil
}

// Highlight is a passage of a post's body marked by a reader. Start and End
// are rune offsets into the body.
type Highlight struct {
	ID        int       `json:"id"`
	PostID    int       `json:"post_id"`
	UserID    int       `json:"user_id"`
	Text      string    `json:"text"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	CreatedAt time.Time `json:"created_at"`
}

// TopHighlight is a passage and how many readers highlighted it.
type TopHighlight struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Count int    `json:"count"`
}

// Highlights is what a reader sees on a post.
type Highlights struct {
	Top  []TopHighlight `json:"top"`
	Mine []Highlight    `json:"mine"`
}

const maxHighlight = 1000

// NewHighlight checks that [start, end) is a passage of body and takes its
// text from body, so clients cannot store arbitrary text.
func NewHighlight(body string, postID, userID, start, end int) (Highlight, error) {
	runes := []rune(body)
	switch {
	case start < 0 || end <= start || end > len(runes):
		return Highlight{}, domain.Invalid("bad start/end")
	case end-start > maxHighlight:
		return Highlight{}, domain.Invalid("highlight too long")
	}
	text := strings.TrimSpace(string(runes[start:end]))
	if text == "" {
		return Highlight{}, domain.Invalid("empty highlight")
	}
	return Highlight{PostID: postID, UserID: userID, Text: text, Start: start, End: end}, nil
}

// AuthorStat is one post's numbers for its author.
type AuthorStat struct {
	PostID      int        `json:"post_id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Status      Status     `json:"status"`
	PublishedAt *time.Time `json:"published_at"`
	Views       int64      `json:"views"`
	Likes       int        `json:"likes"`
	Claps       int        `json:"claps"`
	Comments    int        `json:"comments"`
	Bookmarks   int        `json:"bookmarks"`
	Highlights  int        `json:"highlights"`
	Reads       int        `json:"reads"`
	ReadRatio   float64    `json:"read_ratio"` // reads / views
}

// DailyStat is one day of a post's views and reads.
type DailyStat struct {
	Date  string `json:"date"` // YYYY-MM-DD, UTC
	Views int64  `json:"views"`
	Reads int    `json:"reads"`
}

type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

var (
	ErrUnknownCategory = domain.Invalid("category not found")
	ErrUnknownLabel    = domain.Invalid("label not found")
)

// Repository returns domain.ErrNotFound for a missing post and
// ErrUnknownCategory / ErrUnknownLabel for bad references.
type Repository interface {
	List(ctx context.Context, f Filter) (Page, error)
	// ListByIDs returns published posts in the order of ids.
	ListByIDs(ctx context.Context, ids []int) ([]Post, error)
	// Get and GetBySlug fill Liked and Bookmarked for viewerID (0 = anonymous).
	Get(ctx context.Context, id, viewerID int) (Post, error)
	GetBySlug(ctx context.Context, slug string, viewerID int) (Post, error)
	// Create sets slug, reading time and published_at (when published).
	Create(ctx context.Context, d Draft, authorID int, author string) (Post, error)
	// Update keeps the slug; published_at is set on first publish.
	Update(ctx context.Context, id int, d Draft) (Post, error)
	Delete(ctx context.Context, id int) error
	// Publish moves a scheduled post whose time has come to published and
	// reports whether it did.
	Publish(ctx context.Context, id int, now time.Time) (bool, error)
	DueScheduled(ctx context.Context, now time.Time) ([]int, error)
	// Like and Unlike are idempotent per user; created reports a new like.
	Like(ctx context.Context, id, userID int) (p Post, created bool, err error)
	// Clap adds n claps, capped at MaxClaps per user; created reports the
	// user's first clap on the post.
	Clap(ctx context.Context, id, userID, n int) (p Post, created bool, err error)
	// Unlike removes all of the user's claps.
	Unlike(ctx context.Context, id, userID int) (Post, error)

	AddHighlight(ctx context.Context, h Highlight) (Highlight, error)
	GetHighlight(ctx context.Context, id int) (Highlight, error)
	DeleteHighlight(ctx context.Context, id int) error
	// Highlights returns the most highlighted passages and viewerID's own.
	Highlights(ctx context.Context, postID, viewerID, top int) (Highlights, error)

	// Read records a read once per reader key and reports whether it is new.
	Read(ctx context.Context, postID, userID int, reader string) (bool, error)
	// DailyReads counts reads per UTC day (YYYY-MM-DD) since since.
	DailyReads(ctx context.Context, postID int, since time.Time) (map[string]int, error)
	// AuthorStats lists userID's posts, newest first; Views is left zero.
	AuthorStats(ctx context.Context, userID int, p domain.Paging) ([]AuthorStat, int, error)
	Tags(ctx context.Context) ([]TagCount, error)

	Labels(ctx context.Context) ([]Label, error)
	CreateLabel(ctx context.Context, l Label) (Label, error)
	DeleteLabel(ctx context.Context, id int) error
}

func (p Page) Paginated() (any, any) {
	return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, p.NextCursor)
}
