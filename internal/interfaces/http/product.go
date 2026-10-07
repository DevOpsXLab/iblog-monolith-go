package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/captcha"
	"go.uber.org/zap"
)

// errCaptcha is a failed or missing CAPTCHA on signup.
var errCaptcha = errors.New("captcha")

// humanCheck refuses signups from bots: the honeypot must stay empty and,
// when CAPTCHA is on, the Turnstile token must verify. If Cloudflare is
// unreachable the signup goes through (logged): the per-IP rate limit still
// applies and a CAPTCHA outage must not stop every signup.
func (h *Handler) humanCheck(w http.ResponseWriter, r *http.Request, token, honeypot string) bool {
	if honeypot != "" {
		writeError(w, r, http.StatusBadRequest, "captcha failed")
		return false
	}
	if h.Captcha == nil {
		return true
	}
	err := h.Captcha(r.Context(), token, h.clientIP(r))
	switch {
	case err == nil:
		return true
	case errors.Is(err, captcha.ErrFailed):
		writeError(w, r, http.StatusBadRequest, "captcha failed")
		return false
	default:
		zap.L().Warn("captcha unavailable; signup allowed", zap.Error(err))
		return true
	}
}

// trackPublish records the funnel's last step when a post goes public.
func (h *Handler) trackPublish(r *http.Request, p post.Post, err error) {
	if err == nil && p.IsPublic() {
		h.Analytics.Record(r.Context(), application.EventPublish, p.UserID, p.ID)
	}
}

type publicConfig struct {
	// CaptchaSiteKey is the Turnstile site key; empty = no CAPTCHA on signup.
	CaptchaSiteKey string `json:"captcha_site_key"`
	// Analytics tells whether POST /api/events is recorded.
	Analytics bool `json:"analytics"`
}

// publicConfig is what the client needs to know about this deployment.
//
// Auth: none.
//
// spector:tags ops
func (h *Handler) publicConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, publicConfig{CaptchaSiteKey: h.CaptchaSiteKey, Analytics: h.Analytics != nil})
}

// trackEvent records a product analytics event.
//
// name is page_view, read_time (post_id and value = seconds required) or
// signup_open. visitor is a random id (8-64 letters, digits, dashes) the
// client keeps only after the reader accepts analytics cookies; send
// nothing without consent. The signed-in user is attached from the session.
// No IP or user agent is stored; referrer is reduced to its host.
// Rate limit: 120 per minute.
// Auth: optional.
//
// spector:tags analytics
func (h *Handler) trackEvent(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, "events", 120, time.Minute) {
		return
	}
	if h.Analytics == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var e application.AnalyticsEvent
	// sendBeacon posts text/plain; the body is JSON either way.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&e); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid json")
		return
	}
	e.UserID = h.viewerID(r)
	noContent(w, r, h.Analytics.Track(r.Context(), e), "")
}

// analytics is the product dashboard: DAU, WAU/MAU, signups, average read
// time, the signup funnel (visit, signup form, signup, first story) and
// D1/D7/D30 retention of new users.
//
// Query: days (1-365, default 30).
// Auth: stats.read.
//
// spector:tags admin
func (h *Handler) analytics(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	if h.Analytics == nil {
		writeError(w, r, http.StatusNotFound, "analytics disabled")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days == 0 {
		days = 30
	}
	rep, err := h.Analytics.Report(r.Context(), days)
	respond(w, r, http.StatusOK, rep, err, "")
}

// Prerendering for crawlers

type pageMeta struct {
	Site        string
	Title       string
	Description string
	URL         string
	Image       string
	Type        string // website, article, profile
	Author      string
	Published   string
	Modified    string
	Tags        []string
	Body        template.HTML
	JSONLD      template.JS
	Feed        string
}

var prerenderTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.URL}}">
<meta property="og:site_name" content="{{.Site}}">
<meta property="og:type" content="{{.Type}}">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.URL}}">
{{- if .Image}}
<meta property="og:image" content="{{.Image}}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{{.Image}}">
{{- else}}
<meta name="twitter:card" content="summary">
{{- end}}
<meta name="twitter:title" content="{{.Title}}">
<meta name="twitter:description" content="{{.Description}}">
{{- if .Published}}
<meta property="article:published_time" content="{{.Published}}">
<meta property="article:modified_time" content="{{.Modified}}">
<meta property="article:author" content="{{.Author}}">
{{- range .Tags}}
<meta property="article:tag" content="{{.}}">
{{- end}}
{{- end}}
{{- if .Feed}}
<link rel="alternate" type="application/rss+xml" href="{{.Feed}}">
{{- end}}
{{- if .JSONLD}}
<script type="application/ld+json">{{.JSONLD}}</script>
{{- end}}
</head>
<body>
<main>
<h1>{{.Title}}</h1>
{{- if .Author}}<p>{{.Author}}</p>{{end}}
{{.Body}}
</main>
</body>
</html>
`))

// prerender serves crawlers (search engines, link previews in Telegram,
// Slack, X, Facebook) a static page with title, description, Open Graph,
// Twitter card, canonical link and JSON-LD for a client route. The client's
// nginx sends known bot user agents here; people get the SPA.
//
// path is the client route without the leading slash: "", "@user",
// "@user/slug", "p/slug" or "tag/name".
// Auth: none.
//
// spector:tags feeds
func (h *Handler) prerender(w http.ResponseWriter, r *http.Request) {
	m, err := h.pageFor(r.Context(), strings.Trim(r.PathValue("path"), "/"))
	status := http.StatusOK
	if err != nil {
		status = http.StatusNotFound
		m = pageMeta{Title: "Not found", Description: "This page does not exist.", URL: h.SiteURL + "/", Type: "website"}
	}
	m.Site = h.siteName()
	if m.Title != m.Site && status == http.StatusOK {
		m.Title += " — " + m.Site
	}
	var buf bytes.Buffer
	if err := prerenderTmpl.Execute(&buf, m); err != nil {
		writeError(w, r, http.StatusInternalServerError, "render failed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Robots-Tag", "all")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (h *Handler) siteName() string {
	if h.SiteName != "" {
		return h.SiteName
	}
	return "iBlog"
}

func (h *Handler) pageFor(ctx context.Context, path string) (pageMeta, error) {
	site := h.siteName()
	switch {
	case path == "":
		return pageMeta{
			Title: site, Type: "website", URL: h.SiteURL + "/", Feed: h.SiteURL + "/api/feed.xml",
			Description: site + " — stories, ideas and expertise from writers on any topic.",
		}, nil
	case strings.HasPrefix(path, "p/"):
		return h.postPage(ctx, strings.TrimPrefix(path, "p/"))
	case strings.HasPrefix(path, "tag/"):
		tag := strings.TrimPrefix(path, "tag/")
		return pageMeta{
			Title: "#" + tag, Type: "website", URL: h.SiteURL + "/tag/" + tag,
			Description: "Stories about " + tag + " on " + site + ".",
		}, nil
	case strings.HasPrefix(path, "@"):
		username, slug, _ := strings.Cut(strings.TrimPrefix(path, "@"), "/")
		if slug != "" {
			return h.postPage(ctx, slug)
		}
		prof, err := h.Accounts.Profile(ctx, username, 0)
		if err != nil {
			return pageMeta{}, err
		}
		name := prof.DisplayName
		if name == "" {
			name = prof.Username
		}
		desc := prof.Bio
		if desc == "" {
			desc = "Stories by " + name + " on " + site + "."
		}
		return pageMeta{
			Title: name, Type: "profile", URL: h.SiteURL + "/@" + prof.Username, Image: h.absURL(prof.AvatarURL),
			Description: clipRunes(desc, 200), Feed: h.SiteURL + "/api/users/" + prof.Username + "/feed.xml",
		}, nil
	}
	return pageMeta{}, errors.New("unknown route")
}

func (h *Handler) postPage(ctx context.Context, slug string) (pageMeta, error) {
	p, err := h.Blog.GetPostBySlug(ctx, slug, 0)
	if err != nil {
		return pageMeta{}, err
	}
	desc := p.Subtitle
	if desc == "" {
		desc = plainText(p.Body)
	}
	desc = clipRunes(desc, 200)
	url := h.postURL(p)
	if p.CanonicalURL != "" {
		url = p.CanonicalURL
	}
	pub := p.CreatedAt
	if p.PublishedAt != nil {
		pub = *p.PublishedAt
	}
	m := pageMeta{
		Title: p.Title, Description: desc, URL: url, Image: h.absURL(p.CoverURL), Type: "article",
		Author: p.Author, Published: pub.UTC().Format(time.RFC3339), Modified: p.UpdatedAt.UTC().Format(time.RFC3339),
		Tags: p.Tags, Body: template.HTML(p.BodyHTML), // sanitized by the markdown renderer
	}
	ld := map[string]any{
		"@context": "https://schema.org", "@type": "Article",
		"headline": clipRunes(p.Title, 110), "description": desc, "mainEntityOfPage": url,
		"datePublished": m.Published, "dateModified": m.Modified,
		"author":    map[string]any{"@type": "Person", "name": p.Author, "url": h.SiteURL + "/@" + p.Author},
		"publisher": map[string]any{"@type": "Organization", "name": h.siteName(), "url": h.SiteURL},
		"keywords":  strings.Join(p.Tags, ", "),
	}
	if m.Image != "" {
		ld["image"] = m.Image
	}
	if b, err := json.Marshal(ld); err == nil {
		m.JSONLD = template.JS(b) // json.Marshal escapes <, > and & so it can't close the script
	}
	return m, nil
}

// absURL makes an upload path (/api/uploads/...) absolute for link previews.
func (h *Handler) absURL(u string) string {
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.TrimRight(h.SiteURL, "/") + "/" + strings.TrimLeft(u, "/")
}

var (
	mdLink   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdMarks  = regexp.MustCompile("[#*_>`~|]+")
	mdSpaces = regexp.MustCompile(`\s+`)
)

// plainText is a rough Markdown-to-text for descriptions.
func plainText(md string) string {
	if len(md) > 4000 {
		md = md[:4000]
	}
	md = mdLink.ReplaceAllString(md, "$1")
	md = mdMarks.ReplaceAllString(md, " ")
	return strings.TrimSpace(mdSpaces.ReplaceAllString(md, " "))
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
