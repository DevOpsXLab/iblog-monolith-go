package domain_test

import (
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/internal/domain/relation"
	"github.com/iBlog/iblog-monolith-go/internal/domain/revision"
	"github.com/iBlog/iblog-monolith-go/internal/domain/series"
	"github.com/iBlog/iblog-monolith-go/internal/domain/social"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

func TestKindsValid(t *testing.T) {
	for _, k := range []social.HideKind{social.HidePost, social.HideAuthor, social.HideTag} {
		if !k.Valid() {
			t.Errorf("hide kind %q invalid", k)
		}
	}
	for _, k := range []relation.Kind{relation.Block, relation.Mute} {
		if !k.Valid() {
			t.Errorf("relation %q invalid", k)
		}
	}
	if social.HideKind("user").Valid() || relation.Kind("follow").Valid() || social.HideKind("").Valid() {
		t.Error("unknown kind accepted")
	}
}

func TestSeriesInputNormalize(t *testing.T) {
	in, err := series.SeriesInput{Title: "  Go  ", Description: " basics "}.Normalize()
	if err != nil || in.Title != "Go" || in.Description != "basics" {
		t.Fatalf("normalize = %+v, %v", in, err)
	}
	for name, bad := range map[string]series.SeriesInput{
		"empty title":      {Title: "   "},
		"long title":       {Title: strings.Repeat("t", 201)},
		"long description": {Title: "ok", Description: strings.Repeat("d", 1001)},
	} {
		if _, err := bad.Normalize(); !isInvalid(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRevisionContentEqual(t *testing.T) {
	a := revision.Content{Title: "t", Subtitle: "s", Body: "b"}
	if !a.Equal(a) {
		t.Error("content not equal to itself")
	}
	for _, b := range []revision.Content{{Title: "T", Subtitle: "s", Body: "b"}, {Title: "t", Body: "b"}, {Title: "t", Subtitle: "s", Body: "b\n"}} {
		if a.Equal(b) {
			t.Errorf("%+v equals %+v", a, b)
		}
	}
}

func TestUserPagePaginated(t *testing.T) {
	items, meta := user.Page{Items: []user.User{{ID: 1}}, Total: 21, Page: 2, Limit: 10}.Paginated()
	if us, ok := items.([]user.User); !ok || len(us) != 1 {
		t.Errorf("items = %#v", items)
	}
	if meta == nil {
		t.Error("no meta")
	}
}
