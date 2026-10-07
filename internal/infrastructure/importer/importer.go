// Package importer reads a story from a public web page and converts its
// article to Markdown (application.Importer). Requests to private, loopback
// and link-local addresses are refused, so users cannot make the server
// fetch internal services (SSRF).
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/html"
)

const (
	maxPage      = 2 << 20
	maxRedirects = 3
	timeout      = 10 * time.Second
)

var ErrForbiddenAddress = domain.Invalid("url points to a private address")

type Importer struct{ client *http.Client }

// New builds an importer; allowPrivate lifts the address check (tests).
func New(allowPrivate bool) *Importer {
	dialer := &net.Dialer{Timeout: timeout}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !public(ip) {
				return ErrForbiddenAddress
			}
			return nil
		}
	}
	return &Importer{client: &http.Client{
		Timeout: timeout,
		Transport: otelhttp.NewTransport(&http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		}),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("bad redirect")
			}
			return nil
		},
	}}
}

// public reports whether ip is a routable internet address.
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can reach IPv4 private ranges
}

func (im *Importer) Fetch(ctx context.Context, raw string) (application.Imported, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return application.Imported{}, domain.Invalid("url: http(s) address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return application.Imported{}, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "iBlog-Importer/1.0")
	res, err := im.client.Do(req)
	if errors.Is(err, ErrForbiddenAddress) {
		return application.Imported{}, ErrForbiddenAddress
	}
	if err != nil {
		return application.Imported{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return application.Imported{}, fmt.Errorf("status %d", res.StatusCode)
	}
	if mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type")); mt != "text/html" && mt != "application/xhtml+xml" {
		return application.Imported{}, domain.Invalid("url is not an HTML page")
	}
	return Parse(io.LimitReader(res.Body, maxPage), res.Request.URL)
}

// Parse extracts the story from an HTML page found at base.
func Parse(r io.Reader, base *url.URL) (application.Imported, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return application.Imported{}, err
	}
	m := meta(doc)
	out := application.Imported{
		Title:        first(m["og:title"], m["twitter:title"], text(find(doc, "title"))),
		Subtitle:     first(m["og:description"], m["description"]),
		CanonicalURL: base.String(),
	}
	if c := resolve(base, m["canonical"]); c != "" {
		out.CanonicalURL = c
	}
	if img := resolve(base, m["og:image"]); strings.HasPrefix(img, "https://") {
		out.CoverURL = img
	}
	root := find(doc, "article")
	if root == nil {
		root = find(doc, "main")
	}
	if root == nil {
		root = find(doc, "body")
	}
	if root == nil {
		return out, domain.Invalid("page has no content")
	}
	c := converter{base: base}
	c.children(root)
	out.Body = c.result()
	// The page title often repeats as the article's first heading.
	if h := "# " + out.Title; out.Title != "" && strings.HasPrefix(out.Body, h+"\n") {
		out.Body = strings.TrimSpace(strings.TrimPrefix(out.Body, h))
	}
	if out.Title == "" {
		if h := find(root, "h1"); h != nil {
			out.Title = text(h)
		}
	}
	if strings.TrimSpace(out.Body) == "" {
		return out, domain.Invalid("page has no content")
	}
	return out, nil
}

func first(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// meta collects <meta> (property or name → content) and the canonical link.
func meta(doc *html.Node) map[string]string {
	m := map[string]string{}
	walk(doc, func(n *html.Node) {
		switch n.Data {
		case "meta":
			key := strings.ToLower(first(attr(n, "property"), attr(n, "name")))
			if key != "" && m[key] == "" {
				m[key] = attr(n, "content")
			}
		case "link":
			if strings.EqualFold(attr(n, "rel"), "canonical") && m["canonical"] == "" {
				m["canonical"] = attr(n, "href")
			}
		}
	})
	return m
}

func walk(n *html.Node, fn func(*html.Node)) {
	if n.Type == html.ElementNode {
		fn(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func find(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, tag); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// resolve makes ref absolute against base; only http(s) results are kept.
func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}
