package post

import (
	"testing"
	"time"
)

func TestIsRead(t *testing.T) {
	for _, c := range []struct {
		progress float64
		seconds  int
		minutes  int
		want     bool
	}{
		{0.6, 0, 5, true},
		{0.5, 0, 5, false},
		{0, 150, 5, true},
		{0, 149, 5, false},
		{0, 30, 0, true},
	} {
		if got := IsRead(c.progress, c.seconds, c.minutes); got != c.want {
			t.Errorf("IsRead(%v, %d, %d) = %v", c.progress, c.seconds, c.minutes, got)
		}
	}
	if ValidateRead(1.5, 0) == nil || ValidateRead(0.5, -1) == nil || ValidateRead(1, 10) != nil {
		t.Error("ValidateRead")
	}
}

func TestUnlistedAndCanonical(t *testing.T) {
	now := time.Now()
	d, err := Draft{Title: "x", Status: StatusUnlisted, PublishAt: &now}.Normalize(now)
	if err != nil || d.PublishAt != nil {
		t.Fatalf("unlisted: %v %+v", err, d)
	}
	if !(Post{Status: StatusUnlisted}).IsReadable() || (Post{Status: StatusUnlisted}).IsPublic() {
		t.Error("unlisted readable but not public")
	}
	if _, err := (Draft{Title: "x", CanonicalURL: "javascript:alert(1)"}).Normalize(now); err == nil {
		t.Error("bad canonical_url accepted")
	}
	if _, err := (Draft{Title: "x", CanonicalURL: " https://a.dev/p "}).Normalize(now); err != nil {
		t.Error(err)
	}
}

func TestDraftKeepsEditableFields(t *testing.T) {
	p := Post{Title: "t", Body: "b", Status: StatusPublished, CategoryID: 2, PublicationID: 3,
		Tags: []string{"k8s"}, Labels: []Label{{ID: 7}, {ID: 9}}}
	d := p.Draft()
	if d.Title != "t" || d.CategoryID != 2 || d.PublicationID != 3 || len(d.Tags) != 1 ||
		len(d.LabelIDs) != 2 || d.LabelIDs[1] != 9 {
		t.Errorf("Draft() = %+v", d)
	}
}
