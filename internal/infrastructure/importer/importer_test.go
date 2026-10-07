package importer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

const page = `<!doctype html><html><head>
<title>Site title</title>
<meta property="og:title" content="Deploying Go">
<meta name="description" content="A short guide">
<meta property="og:image" content="/cover.png">
<link rel="canonical" href="https://blog.example/deploying-go">
<script>alert(1)</script>
</head><body>
<nav><a href="/">Home</a></nav>
<article>
<h1>Deploying Go</h1>
<p>Build a <strong>static</strong> binary with <code>go build</code>, see <a href="/docs">docs</a>.</p>
<h2>Steps</h2>
<ol><li>Build</li><li>Ship</li></ol>
<ul><li>fast</li><li>small_*</li></ul>
<blockquote><p>Simple is better.</p></blockquote>
<pre><code class="language-sh">go build ./...
docker build .</code></pre>
<img src="img/a.png" alt="diagram">
<aside>Ads</aside>
</article>
<footer>copyright</footer>
</body></html>`

func TestParse(t *testing.T) {
	base, _ := url.Parse("https://blog.example/posts/1")
	got, err := Parse(strings.NewReader(page), base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Deploying Go" || got.Subtitle != "A short guide" {
		t.Errorf("meta = %+v", got)
	}
	if got.CoverURL != "https://blog.example/cover.png" || got.CanonicalURL != "https://blog.example/deploying-go" {
		t.Errorf("urls = %q %q", got.CoverURL, got.CanonicalURL)
	}
	want := "Build a **static** binary with `go build`, see [docs](https://blog.example/docs).\n\n" +
		"## Steps\n\n1. Build\n2. Ship\n\n- fast\n- small\\_\\*\n\n> Simple is better.\n\n" +
		"```sh\ngo build ./...\ndocker build .\n```\n\n![diagram](https://blog.example/posts/img/a.png)"
	if got.Body != want {
		t.Errorf("body:\n%s\n--- want:\n%s", got.Body, want)
	}
	for _, bad := range []string{"alert", "Home", "Ads", "copyright"} {
		if strings.Contains(got.Body, bad) {
			t.Errorf("body has %q", bad)
		}
	}
}

func TestFetchRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page))
	}))
	defer srv.Close()

	if _, err := New(false).Fetch(context.Background(), srv.URL); !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("loopback fetch: %v", err)
	}
	got, err := New(true).Fetch(context.Background(), srv.URL)
	if err != nil || got.Title != "Deploying Go" {
		t.Fatalf("allowed fetch: %v %+v", err, got)
	}
	if _, err := New(true).Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("file url accepted")
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "0.0.0.0": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v", addr, got)
		}
	}
}
