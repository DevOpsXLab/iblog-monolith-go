// Package mention finds @username mentions in text.
package mention

import (
	"regexp"
	"slices"
	"strings"
)

// Max is how many users one text may notify.
const Max = 20

// A mention is @ followed by a username, not preceded by a word character
// or another @ (so emails and @@ do not count).
var re = regexp.MustCompile(`(?:^|[^\w@])@([A-Za-z0-9_]{3,32})\b`)

// Parse returns the distinct lower-cased usernames mentioned in text, in
// order, at most Max. Code spans and fenced blocks are skipped.
func Parse(text string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(stripCode(text), -1) {
		u := strings.ToLower(m[1])
		if !slices.Contains(out, u) {
			out = append(out, u)
			if len(out) == Max {
				break
			}
		}
	}
	return out
}

// New returns the mentions in cur that were not already in old.
func New(old, cur string) []string {
	before := Parse(old)
	var out []string
	for _, u := range Parse(cur) {
		if !slices.Contains(before, u) {
			out = append(out, u)
		}
	}
	return out
}

var codeRe = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

func stripCode(s string) string { return codeRe.ReplaceAllString(s, " ") }
