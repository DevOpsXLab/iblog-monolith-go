package application

import "testing"

type countingMD struct{ n int }

func (m *countingMD) Render(body string) string { m.n++; return "<p>" + body + "</p>" }

func TestRenderCache(t *testing.T) {
	md := &countingMD{}
	c := newRenderCache(2)
	for _, b := range []string{"a", "a", "b", "c", "a"} {
		if got := c.render(md, b); got != "<p>"+b+"</p>" {
			t.Fatalf("render %q = %q", b, got)
		}
	}
	// a, b, c miss; second a hits; last a was evicted by c (max 2).
	if md.n != 4 {
		t.Fatalf("renders = %d, want 4", md.n)
	}
	var nilCache *renderCache
	if nilCache.render(md, "x") != "<p>x</p>" {
		t.Fatal("nil cache must render")
	}
}
