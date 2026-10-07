package application

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

// Product analytics event names. Clients may send only ClientEvents; the
// server records signup and publish itself so the funnel can't be faked or
// lost to an ad blocker.
const (
	EventPageView   = "page_view"
	EventReadTime   = "read_time"   // value: seconds actively on a post
	EventSignupOpen = "signup_open" // the signup form was shown
	EventSignup     = "signup"      // server-side
	EventPublish    = "publish"     // server-side: a post went public
)

// ClientEvents are the names POST /api/events accepts.
var ClientEvents = map[string]bool{EventPageView: true, EventReadTime: true, EventSignupOpen: true}

// AnalyticsRetention is how long raw events are kept.
const AnalyticsRetention = 400 * 24 * time.Hour

// MaxReadSeconds caps one read_time event (a tab left open overnight).
const MaxReadSeconds = 60 * 60

var visitorRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// AnalyticsEvent is one tracked product event.
type AnalyticsEvent struct {
	Name     string `json:"name"`
	Visitor  string `json:"visitor"`
	UserID   int    `json:"-"`
	Path     string `json:"path"`
	PostID   int    `json:"post_id"`
	Value    int    `json:"value"`
	Lang     string `json:"lang"`
	Referrer string `json:"referrer"`
}

// DayCount is one point of a daily series.
type DayCount struct {
	Day   string `json:"day"` // YYYY-MM-DD, UTC
	Count int    `json:"count"`
}

// FunnelStep is one step of the signup funnel (distinct visitors/users).
type FunnelStep struct {
	Step  string `json:"step"`
	Count int    `json:"count"`
}

// Retention is the share of a signup cohort that came back.
type Retention struct {
	Cohort int     `json:"cohort"` // users who signed up in the window (old enough to measure)
	D1     float64 `json:"d1"`     // active on day 1 after signup
	D7     float64 `json:"d7"`     // active on day 7
	D30    float64 `json:"d30"`    // active on day 30
}

// AnalyticsReport is the product dashboard for the last Days days.
type AnalyticsReport struct {
	Days           int          `json:"days"`
	DAU            []DayCount   `json:"dau"` // distinct visitors per day
	WAU            int          `json:"wau"` // distinct visitors, last 7 days
	MAU            int          `json:"mau"` // distinct visitors, last 30 days
	Signups        []DayCount   `json:"signups"`
	AvgReadSeconds float64      `json:"avg_read_seconds"`
	TotalReadHours float64      `json:"total_read_hours"`
	Funnel         []FunnelStep `json:"funnel"`
	Retention      Retention    `json:"retention"`
	TopPaths       []PathCount  `json:"top_paths"`
	Languages      []PathCount  `json:"languages"`
}

// PathCount is a page or language with its distinct visitors.
type PathCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// AnalyticsRepo stores and aggregates events.
type AnalyticsRepo interface {
	Insert(ctx context.Context, e AnalyticsEvent) error
	Report(ctx context.Context, since time.Time, days int) (AnalyticsReport, error)
	PurgeBefore(ctx context.Context, before time.Time) (int64, error)
}

// Analytics records product events and reports DAU, retention, read time
// and the signup funnel.
type Analytics struct {
	Repo  AnalyticsRepo
	Authz Authorizer
	now   func() time.Time
}

func NewAnalytics(repo AnalyticsRepo, authz Authorizer) *Analytics {
	return &Analytics{Repo: repo, Authz: authz, now: time.Now}
}

// Track validates and stores a client event. Unknown names are refused.
func (a *Analytics) Track(ctx context.Context, e AnalyticsEvent) error {
	if !ClientEvents[e.Name] {
		return domain.Invalid("unknown event")
	}
	return a.record(ctx, e)
}

// Record stores a server-side event (signup, publish). It never fails the
// caller: analytics must not break a signup.
func (a *Analytics) Record(ctx context.Context, name string, userID, postID int) {
	if a == nil {
		return
	}
	_ = a.record(ctx, AnalyticsEvent{Name: name, Visitor: "u" + itoa(userID), UserID: userID, PostID: postID})
}

func (a *Analytics) record(ctx context.Context, e AnalyticsEvent) error {
	ctx, span := tracer.Start(ctx, "Analytics.Track")
	defer span.End()
	if e.UserID == 0 && !visitorRe.MatchString(e.Visitor) {
		return domain.Invalid("visitor: 8-64 letters, digits or dashes")
	}
	if e.UserID != 0 && e.Visitor == "" {
		e.Visitor = "u" + itoa(e.UserID)
	}
	e.Path = clip(e.Path, 300)
	if e.Path != "" && !strings.HasPrefix(e.Path, "/") {
		return domain.Invalid("path must start with /")
	}
	e.Lang = clip(e.Lang, 8)
	e.Referrer = clip(refHost(e.Referrer), 120)
	if e.Name == EventReadTime {
		if e.PostID <= 0 || e.Value <= 0 {
			return domain.Invalid("read_time needs post_id and value > 0")
		}
		e.Value = min(e.Value, MaxReadSeconds)
	} else {
		e.Value = max(e.Value, 0)
	}
	return a.Repo.Insert(ctx, e)
}

// Report needs stats.read (admins). days is clamped to 1..365.
func (a *Analytics) Report(ctx context.Context, days int) (AnalyticsReport, error) {
	ctx, span := tracer.Start(ctx, "Analytics.Report")
	defer span.End()
	if err := a.Authz.Can(ctx, "stats", "read", 0, 0); err != nil {
		return AnalyticsReport{}, err
	}
	days = min(max(days, 1), 365)
	now := a.now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	return a.Repo.Report(ctx, since, days)
}

// PurgeOld drops events past AnalyticsRetention (worker sweep).
func (a *Analytics) PurgeOld(ctx context.Context) error {
	if a == nil {
		return nil
	}
	_, err := a.Repo.PurgeBefore(ctx, a.now().Add(-AnalyticsRetention))
	return err
}

// refHost keeps only the referring host: full URLs can carry tokens.
func refHost(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if _, rest, ok := strings.Cut(ref, "://"); ok {
		ref = rest
	}
	host, _, _ := strings.Cut(ref, "/")
	host, _, _ = strings.Cut(host, "?")
	return strings.ToLower(host)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
