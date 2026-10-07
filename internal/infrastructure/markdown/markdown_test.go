package markdown

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	r := New()
	cases := []struct{ in, want, notWant string }{
		{"# Hello", `<h1 id="hello">Hello</h1>`, ""},
		{"**b** ~~s~~", "<strong>b</strong> <del>s</del>", ""},
		{"<script>alert(1)</script>hi", "hi", "<script"},
		{`<a href="javascript:alert(1)">x</a>`, "x", "javascript:"},
		{`<img src=x onerror=alert(1)>`, "<img", "onerror"},
		{"```go\nfmt.Println()\n```", `<code class="language-go">`, ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", `src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`, ""},
		{"https://vimeo.com/76979871", `src="https://player.vimeo.com/video/76979871"`, ""},
		{"see https://youtu.be/dQw4w9WgXcQ here", "", "<iframe"},  // not alone on its line
		{"```\nhttps://youtu.be/dQw4w9WgXcQ\n```", "", "<iframe"}, // inside code
		{`<iframe src="https://evil.example/x"></iframe>`, "", "evil"},
		{"[x](https://example.com)", `rel="nofollow`, ""},
	}
	for _, c := range cases {
		got := r.Render(c.in)
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("Render(%q) = %q, want %q", c.in, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("Render(%q) = %q, must not contain %q", c.in, got, c.notWant)
		}
	}
}
