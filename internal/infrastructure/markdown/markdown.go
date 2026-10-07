// Package markdown renders post bodies to safe HTML: GitHub-flavoured
// Markdown, sanitized, with YouTube and Vimeo links on their own line turned
// into embeds.
package markdown

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

type Renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
}

func New() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Raw HTML passes through to the sanitizer, which has the last word.
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-z0-9-]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[\w+-]+$`)).OnElements("code")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^embed$`)).OnElements("div")
	p.AllowElements("iframe")
	p.AllowAttrs("src").Matching(embedSrc).OnElements("iframe")
	p.AllowAttrs("allowfullscreen", "loading", "title").OnElements("iframe")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return &Renderer{md: md, policy: p}
}

var (
	embedSrc = regexp.MustCompile(`^https://(www\.youtube-nocookie\.com/embed/[\w-]{11}|player\.vimeo\.com/video/\d+)$`)
	youtube  = regexp.MustCompile(`^https?://(?:www\.)?(?:youtube\.com/watch\?v=|youtu\.be/)([\w-]{11})\S*$`)
	vimeo    = regexp.MustCompile(`^https?://(?:www\.)?vimeo\.com/(\d+)\S*$`)
)

// Render returns sanitized HTML for a Markdown body.
func (r *Renderer) Render(body string) string {
	var out bytes.Buffer
	if err := r.md.Convert([]byte(embeds(body)), &out); err != nil {
		return ""
	}
	return r.policy.Sanitize(out.String())
}

// embeds replaces a line that is only a YouTube or Vimeo URL (outside code
// fences) with an iframe block.
func embeds(body string) string {
	lines := strings.Split(body, "\n")
	fenced := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		src := ""
		if m := youtube.FindStringSubmatch(t); m != nil {
			src = "https://www.youtube-nocookie.com/embed/" + m[1]
		} else if m := vimeo.FindStringSubmatch(t); m != nil {
			src = "https://player.vimeo.com/video/" + m[1]
		}
		if src != "" {
			lines[i] = "\n<div class=\"embed\"><iframe src=\"" + src + "\" loading=\"lazy\" allowfullscreen></iframe></div>\n"
		}
	}
	return strings.Join(lines, "\n")
}
