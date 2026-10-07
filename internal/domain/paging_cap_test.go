package domain

import (
	"math"
	"testing"
)

func TestNewPagingCapsPage(t *testing.T) {
	p := NewPaging(math.MaxInt, MaxLimit)
	if p.Page != MaxPage {
		t.Fatalf("page = %d, want %d", p.Page, MaxPage)
	}
	if got, want := p.Offset(), (MaxPage-1)*MaxLimit; got != want {
		t.Fatalf("offset = %d, want %d", got, want)
	}
}

func TestOffsetClampsRawPaging(t *testing.T) {
	for _, p := range []Paging{{Page: math.MaxInt, Limit: math.MaxInt}, {Page: -5, Limit: 10}, {Page: 2, Limit: -1}} {
		if o := p.Offset(); o < 0 || o > (MaxPage-1)*MaxLimit {
			t.Errorf("%+v offset = %d", p, o)
		}
	}
}
