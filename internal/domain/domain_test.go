package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/category"
	"github.com/iBlog/iblog-monolith-go/internal/domain/comment"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/iBlog/iblog-monolith-go/internal/domain/publication"
	"github.com/iBlog/iblog-monolith-go/internal/domain/report"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

func isInvalid(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func TestDraftNormalize(t *testing.T) {
	d, err := post.Draft{Title: "  Hi  ", Tags: []string{"Go", " go ", "", "React"}, LabelIDs: []int{3, 1, 3}}.Normalize(now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Hi" || d.Status != post.StatusPublished {
		t.Errorf("got %+v", d)
	}
	if strings.Join(d.Tags, ",") != "go,react" {
		t.Errorf("tags = %v", d.Tags)
	}
	if len(d.LabelIDs) != 2 || d.LabelIDs[0] != 1 {
		t.Errorf("labels = %v", d.LabelIDs)
	}

	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	bad := []post.Draft{
		{Title: "   "},
		{Title: strings.Repeat("x", 201)},
		{Title: "x", Subtitle: strings.Repeat("x", 301)},
		{Title: "x", CategoryID: -1},
		{Title: "x", CoverURL: "javascript:alert(1)"},
		{Title: "x", Tags: strings.Split("a,b,c,d,e,f,g,h,i,j,k", ",")},
		{Title: "x", LabelIDs: []int{1, 2, 3, 4, 5, 6}},
		{Title: "x", Status: "archived"},
		{Title: "x", Status: post.StatusScheduled},
		{Title: "x", Status: post.StatusScheduled, PublishAt: &past},
	}
	for _, d := range bad {
		if _, err := d.Normalize(now); !isInvalid(err) {
			t.Errorf("Normalize(%+v) err = %v, want validation error", d, err)
		}
	}

	d, err = post.Draft{Title: "x", Status: post.StatusScheduled, PublishAt: &future}.Normalize(now)
	if err != nil || d.PublishAt == nil {
		t.Errorf("scheduled: %+v %v", d, err)
	}
	d, _ = post.Draft{Title: "x", Status: post.StatusDraft, PublishAt: &future}.Normalize(now)
	if d.PublishAt != nil {
		t.Error("publish_at must be cleared for drafts")
	}
}

func TestSlugAndReadingTime(t *testing.T) {
	s := post.NewSlug("Hello, World! Go 1.26")
	if !strings.HasPrefix(s, "hello-world-go-1-26-") || len(s) != len("hello-world-go-1-26-")+8 {
		t.Errorf("slug = %q", s)
	}
	if post.NewSlug("Hello") == post.NewSlug("Hello") {
		t.Error("slugs of equal titles must differ")
	}
	if post.ReadingTime("") != 1 || post.ReadingTime(strings.Repeat("w ", 401)) != 3 {
		t.Error("reading time")
	}
}

func TestLabel(t *testing.T) {
	l, err := post.NewLabel(" Guide ", "")
	if err != nil || l.Name != "Guide" || l.Color != "#6b7280" {
		t.Fatalf("got %+v, %v", l, err)
	}
	if l, _ := post.NewLabel("x", "#ABCDEF"); l.Color != "#abcdef" {
		t.Errorf("color = %q", l.Color)
	}
	for _, c := range []string{"red", "#fff", "#gggggg"} {
		if _, err := post.NewLabel("x", c); !isInvalid(err) {
			t.Errorf("color %q accepted", c)
		}
	}
}

func TestCommentNew(t *testing.T) {
	c, err := comment.New(1, 0, 2, "ali", "  hello ")
	if err != nil || c.Text != "hello" || c.UserID != 2 {
		t.Fatalf("got %+v, %v", c, err)
	}
	if _, err := comment.New(1, 0, 2, "ali", "   "); !isInvalid(err) {
		t.Errorf("empty text err = %v", err)
	}
	if _, err := comment.New(1, -1, 2, "ali", "x"); !isInvalid(err) {
		t.Errorf("bad parent err = %v", err)
	}
	if _, err := comment.ValidateText(strings.Repeat("x", 5001)); !isInvalid(err) {
		t.Errorf("long text err = %v", err)
	}
}

func TestRegistration(t *testing.T) {
	r, err := user.NewRegistration(" ali_1 ", "Ali@Example.COM", "secret123")
	if err != nil || r.Username != "ali_1" {
		t.Fatalf("got %+v, %v", r, err)
	}
	cases := []struct{ name, email, pass string }{
		{"ab", "a@example.com", "secret123"},
		{"bad name", "a@example.com", "secret123"},
		{"ali", "not-an-email", "secret123"},
		{"ali", "a@mailinator.com", "secret123"}, // disposable
		{"ali", "a@example.com", "short"},
		{"ali", "a@example.com", strings.Repeat("x", 73)},
	}
	for _, c := range cases {
		if _, err := user.NewRegistration(c.name, c.email, c.pass); !isInvalid(err) {
			t.Errorf("NewRegistration(%q, %q) err = %v", c.name, c.email, err)
		}
	}
}

func TestProfileUpdate(t *testing.T) {
	if _, err := (user.ProfileUpdate{DisplayName: " Ali "}).Normalize(); err != nil {
		t.Fatal(err)
	}
	if _, err := (user.ProfileUpdate{AvatarURL: "ftp://x"}).Normalize(); !isInvalid(err) {
		t.Error("bad avatar accepted")
	}
	if _, err := (user.ProfileUpdate{Bio: strings.Repeat("x", 301)}).Normalize(); !isInvalid(err) {
		t.Error("long bio accepted")
	}
}

func TestCategoryName(t *testing.T) {
	if n, err := category.NormalizeName(" Go "); err != nil || n != "Go" {
		t.Fatalf("got %q, %v", n, err)
	}
	if _, err := category.NormalizeName(" "); !isInvalid(err) {
		t.Errorf("empty err = %v", err)
	}
}

func TestPaging(t *testing.T) {
	cases := []struct{ page, limit, wantPage, wantLimit, wantOffset int }{
		{0, 0, 1, domain.DefaultLimit, 0},
		{3, 5, 3, 5, 10},
		{-1, 1000, 1, domain.MaxLimit, 0},
	}
	for _, c := range cases {
		p := domain.NewPaging(c.page, c.limit)
		if p.Page != c.wantPage || p.Limit != c.wantLimit || p.Offset() != c.wantOffset {
			t.Errorf("NewPaging(%d,%d) = %+v offset %d", c.page, c.limit, p, p.Offset())
		}
	}
}

func TestClapsAndHighlights(t *testing.T) {
	for _, n := range []int{0, -1, 51} {
		if !isInvalid(post.ValidateClaps(n)) {
			t.Errorf("ValidateClaps(%d) passed", n)
		}
	}
	if err := post.ValidateClaps(50); err != nil {
		t.Error(err)
	}

	body := "Ünicode   matters here"
	h, err := post.NewHighlight(body, 1, 2, 0, 10)
	if err != nil || h.Text != "Ünicode" {
		t.Fatalf("got %q, %v", h.Text, err)
	}
	for _, r := range [][2]int{{-1, 3}, {3, 3}, {5, 2}, {0, 100}, {7, 10}} {
		if _, err := post.NewHighlight(body, 1, 2, r[0], r[1]); !isInvalid(err) {
			t.Errorf("NewHighlight %v err = %v", r, err)
		}
	}
}

func TestNewReport(t *testing.T) {
	r, err := report.New(1, report.TargetPost, 5, report.ReasonSpam, "  buy now  ")
	if err != nil || r.Note != "buy now" || r.Status != report.StatusOpen {
		t.Fatalf("got %+v, %v", r, err)
	}
	bad := []struct {
		t      report.TargetType
		id     int
		reason report.Reason
		note   string
	}{
		{"tag", 5, report.ReasonSpam, ""},
		{report.TargetPost, 0, report.ReasonSpam, ""},
		{report.TargetPost, 5, "meh", ""},
		{report.TargetPost, 5, report.ReasonOther, " "},
		{report.TargetPost, 5, report.ReasonSpam, strings.Repeat("x", 1001)},
	}
	for _, b := range bad {
		if _, err := report.New(1, b.t, b.id, b.reason, b.note); !isInvalid(err) {
			t.Errorf("New(%+v) err = %v", b, err)
		}
	}
	if !isInvalid(report.ValidateDecision(report.StatusOpen)) || report.ValidateDecision(report.StatusDismissed) != nil {
		t.Error("ValidateDecision")
	}
}

func TestPublicationRoles(t *testing.T) {
	owner, editor, writer := publication.RoleOwner, publication.RoleEditor, publication.RoleWriter
	cases := []struct {
		actor, target publication.Role
		want          bool
	}{
		{owner, editor, true}, {owner, writer, true}, {owner, owner, false},
		{editor, writer, true}, {editor, editor, false}, {editor, owner, false},
		{writer, writer, false}, {"", writer, false},
	}
	for _, c := range cases {
		if got := c.actor.CanManage(c.target); got != c.want {
			t.Errorf("%q.CanManage(%q) = %v", c.actor, c.target, got)
		}
	}
	if !writer.CanWrite() || writer.CanEdit() || !editor.CanEdit() || publication.Role("").CanWrite() {
		t.Error("CanWrite/CanEdit")
	}

	in, err := publication.PublicationInput{Slug: " Go-Team ", Name: " Go "}.Normalize(true)
	if err != nil || in.Slug != "go-team" || in.Name != "Go" {
		t.Fatalf("got %+v, %v", in, err)
	}
	for _, slug := range []string{"go", "-go", "go-", "go team", strings.Repeat("a", 41)} {
		if _, err := (publication.PublicationInput{Slug: slug, Name: "x"}).Normalize(true); !isInvalid(err) {
			t.Errorf("slug %q accepted", slug)
		}
	}
	if _, err := (publication.PublicationInput{Name: "x"}).Normalize(false); err != nil {
		t.Errorf("update without slug: %v", err)
	}
}

func TestPostCursor(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 30, 0, 123456000, time.UTC)
	c, err := post.ParseCursor(post.CursorAfter(post.Post{ID: 42, PublishedAt: &at}))
	if err != nil || c.ID != 42 || !c.At.Equal(at) {
		t.Fatalf("got %+v, %v", c, err)
	}
	draft := post.Post{ID: 1, UpdatedAt: at}
	if !draft.SortTime().Equal(at) {
		t.Error("draft sorts by updated_at")
	}
	for _, s := range []string{"!!", "YWJj", "MTow"} { // bad base64, "abc", "1:0"
		if _, err := post.ParseCursor(s); !isInvalid(err) {
			t.Errorf("ParseCursor(%q) err = %v", s, err)
		}
	}
}
