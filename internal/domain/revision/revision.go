// Package revision is a post's edit history: snapshots of earlier titles,
// subtitles and bodies, and line diffs between them.
package revision

import (
	"context"
	"strings"
	"time"
)

// Revision is a snapshot of a post before an edit. Version 1 is the
// original text.
type Revision struct {
	ID        int       `json:"id"`
	PostID    int       `json:"post_id"`
	Version   int       `json:"version"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle"`
	Body      string    `json:"body,omitempty"` // single reads only
	EditorID  int       `json:"editor_id"`
	Editor    string    `json:"editor"`
	CreatedAt time.Time `json:"created_at"`
}

// Content is the versioned part of a post.
type Content struct {
	Title    string
	Subtitle string
	Body     string
}

func (c Content) Equal(o Content) bool { return c == o }

// Op is one line of a diff: "=" kept, "-" removed, "+" added.
type Op struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Diff compares a revision with the current text.
type Diff struct {
	Title    []Op `json:"title"`
	Subtitle []Op `json:"subtitle"`
	Body     []Op `json:"body"`
	Added    int  `json:"added"`
	Removed  int  `json:"removed"`
}

// Compare diffs old against cur line by line.
func Compare(old, cur Content) Diff {
	d := Diff{Title: Lines(old.Title, cur.Title), Subtitle: Lines(old.Subtitle, cur.Subtitle), Body: Lines(old.Body, cur.Body)}
	for _, ops := range [][]Op{d.Title, d.Subtitle, d.Body} {
		for _, o := range ops {
			switch o.Op {
			case "+":
				d.Added++
			case "-":
				d.Removed++
			}
		}
	}
	return d
}

// maxCells caps the LCS table; bigger changes fall back to remove-all/add-all
// for the differing middle.
const maxCells = 4_000_000

// Lines is a line diff of a and b (LCS after trimming the common prefix
// and suffix).
func Lines(a, b string) []Op {
	x, y := split(a), split(b)
	ops := []Op{}
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		ops = append(ops, Op{"=", x[pre]})
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	ops = append(ops, middle(x[pre:len(x)-suf], y[pre:len(y)-suf])...)
	for _, l := range x[len(x)-suf:] {
		ops = append(ops, Op{"=", l})
	}
	return ops
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func middle(x, y []string) []Op {
	var ops []Op
	if len(x)*len(y) > maxCells {
		for _, l := range x {
			ops = append(ops, Op{"-", l})
		}
		for _, l := range y {
			ops = append(ops, Op{"+", l})
		}
		return ops
	}
	// lcs[i][j] is the LCS length of x[i:] and y[j:].
	w := len(y) + 1
	lcs := make([]int32, (len(x)+1)*w)
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		switch {
		case x[i] == y[j]:
			ops = append(ops, Op{"=", x[i]})
			i, j = i+1, j+1
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, Op{"-", x[i]})
			i++
		default:
			ops = append(ops, Op{"+", y[j]})
			j++
		}
	}
	for ; i < len(x); i++ {
		ops = append(ops, Op{"-", x[i]})
	}
	for ; j < len(y); j++ {
		ops = append(ops, Op{"+", y[j]})
	}
	return ops
}

// Repository returns domain.ErrNotFound for a missing revision.
type Repository interface {
	// Add stores c as the post's next version.
	Add(ctx context.Context, postID, editorID int, c Content) (Revision, error)
	// List returns the post's revisions, newest first, without bodies.
	List(ctx context.Context, postID int) ([]Revision, error)
	Get(ctx context.Context, postID, version int) (Revision, error)
}
