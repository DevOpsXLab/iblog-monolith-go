package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/mention"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/mfa"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/revision"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/sanction"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/series"
)

// RFC 6238 appendix B (SHA-1), last 6 digits.
func TestTOTPVectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for _, c := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		got, err := mfa.Code(secret, mfa.Step(time.Unix(c.unix, 0)))
		if err != nil || got != c.want {
			t.Errorf("Code at %d = %q, %v; want %q", c.unix, got, err, c.want)
		}
	}
}

func TestTOTPVerifyWindow(t *testing.T) {
	secret := mfa.NewSecret()
	step := mfa.Step(now)
	prev, _ := mfa.Code(secret, step-1)
	if got, ok := mfa.Verify(secret, prev, now); !ok || got != step-1 {
		t.Fatalf("previous step rejected: %d %v", got, ok)
	}
	old, _ := mfa.Code(secret, step-3)
	if _, ok := mfa.Verify(secret, old, now); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := mfa.Verify(secret, "12345", now); ok {
		t.Fatal("short code accepted")
	}
	if !strings.HasPrefix(mfa.URI(secret, "a@b.c"), "otpauth://totp/DevOpsXLab:a@b.c?") {
		t.Fatal(mfa.URI(secret, "a@b.c"))
	}
}

func TestBackupCodes(t *testing.T) {
	codes := mfa.NewBackupCodes()
	if len(codes) != mfa.Backups || len(codes[0]) != 11 || codes[0][5] != '-' {
		t.Fatalf("codes = %v", codes)
	}
	c := codes[0]
	if !mfa.IsBackupCode(c) || mfa.IsBackupCode("123456") {
		t.Fatal("IsBackupCode")
	}
	if mfa.HashBackupCode(c) != mfa.HashBackupCode(" "+strings.ToUpper(strings.ReplaceAll(c, "-", ""))) {
		t.Fatal("hash must ignore case, dashes and spaces")
	}
}

func TestSanctionNew(t *testing.T) {
	later := now.Add(time.Hour)
	s, err := sanction.New(2, 1, " spam ", &later, now)
	if err != nil || s.Kind != sanction.Suspended || s.Reason != "spam" {
		t.Fatalf("suspension: %+v %v", s, err)
	}
	if s, _ := sanction.New(2, 1, "spam", nil, now); s.Kind != sanction.Banned {
		t.Fatal("no until should ban")
	}
	past := now.Add(-time.Minute)
	for name, err := range map[string]error{
		"no reason": func() error { _, err := sanction.New(2, 1, " ", nil, now); return err }(),
		"past":      func() error { _, err := sanction.New(2, 1, "x", &past, now); return err }(),
		"self":      func() error { _, err := sanction.New(1, 1, "x", nil, now); return err }(),
	} {
		if !isInvalid(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMentions(t *testing.T) {
	got := mention.Parse("hi @Alice and @bob_1, mail me@example.com, @@x @al `@code` @alice\n```\n@fenced\n```")
	if !slices.Equal(got, []string{"alice", "bob_1"}) {
		t.Fatalf("Parse = %v", got)
	}
	if got := mention.New("@alice", "@alice @carol"); !slices.Equal(got, []string{"carol"}) {
		t.Fatalf("New = %v", got)
	}
}

func TestRevisionDiff(t *testing.T) {
	d := revision.Compare(
		revision.Content{Title: "T", Body: "a\nb\nc\nd"},
		revision.Content{Title: "T", Body: "a\nB\nc\nd\ne"},
	)
	var ops []string
	for _, o := range d.Body {
		ops = append(ops, o.Op+o.Text)
	}
	want := []string{"=a", "-b", "+B", "=c", "=d", "+e"}
	if !slices.Equal(ops, want) || d.Added != 2 || d.Removed != 1 {
		t.Fatalf("diff = %v (+%d -%d), want %v", ops, d.Added, d.Removed, want)
	}
	if len(d.Title) != 1 || d.Title[0].Op != "=" {
		t.Fatalf("title diff = %v", d.Title)
	}
	if got := revision.Lines("", "x"); len(got) != 1 || got[0].Op != "+" {
		t.Fatalf("from empty = %v", got)
	}
}

func TestSeriesNavigate(t *testing.T) {
	parts := []series.Part{{Position: 1, PostID: 10}, {Position: 2, PostID: 20}, {Position: 3, PostID: 30}}
	n, ok := series.Navigate(series.Series{ID: 1}, parts, 20)
	if !ok || n.Position != 2 || n.Prev.PostID != 10 || n.Next.PostID != 30 {
		t.Fatalf("navigate = %+v", n)
	}
	if n, _ := series.Navigate(series.Series{}, parts, 10); n.Prev != nil {
		t.Fatal("first part has no prev")
	}
	if _, ok := series.Navigate(series.Series{}, parts, 99); ok {
		t.Fatal("missing post found")
	}
	if !isInvalid(series.ValidateOrder([]int{1, 1})) || series.ValidateOrder([]int{3, 1}) != nil {
		t.Fatal("ValidateOrder")
	}
}
