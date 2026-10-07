package http

import (
	"encoding/xml"
	"net/http"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/post"
	"github.com/bakhod1r/errorx"
)

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	Author      string `xml:"author,omitempty"`
	PubDate     string `xml:"pubDate"`
}

type urlSet struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

// postURL is the story's page on the client (/@author/slug, or /p/slug).
func (h *Handler) postURL(p post.Post) string {
	if p.Author != "" {
		return h.SiteURL + "/@" + p.Author + "/" + p.Slug
	}
	return h.SiteURL + "/p/" + p.Slug
}

func writeXML(w http.ResponseWriter, ctype string, v any) {
	w.Header().Set("Content-Type", ctype)
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(v)
}

// rss is the RSS 2.0 feed of the latest 20 published posts.
//
// Auth: none.
//
// spector:tags feeds
func (h *Handler) rss(w http.ResponseWriter, r *http.Request) {
	page, err := h.Blog.ListPosts(r.Context(), post.Filter{Paging: domain.NewPaging(1, 20)})
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	h.writeRSS(w, rssChannel{Title: h.siteName(), Link: h.SiteURL, Description: "Latest posts"}, page.Items)
}

// userRSS is the RSS 2.0 feed of one author's latest 20 published posts.
//
// Auth: none.
//
// spector:tags feeds
func (h *Handler) userRSS(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	prof, err := h.Accounts.Profile(r.Context(), username, 0)
	if err != nil {
		respond(w, r, 0, nil, err, "user not found")
		return
	}
	page, err := h.Accounts.UserPosts(r.Context(), username, domain.NewPaging(1, 20))
	if err != nil {
		respond(w, r, 0, nil, err, "user not found")
		return
	}
	title := prof.DisplayName
	if title == "" {
		title = prof.Username
	}
	desc := prof.Bio
	if desc == "" {
		desc = "Posts by " + title
	}
	h.writeRSS(w, rssChannel{Title: title + " — " + h.siteName(), Link: h.SiteURL + "/@" + prof.Username, Description: desc}, page.Items)
}

func (h *Handler) writeRSS(w http.ResponseWriter, ch rssChannel, posts []post.Post) {
	feed := rssFeed{Version: "2.0", Channel: ch}
	for _, p := range posts {
		pub := p.CreatedAt
		if p.PublishedAt != nil {
			pub = *p.PublishedAt
		}
		desc := p.Subtitle
		if desc == "" {
			desc = p.Title
		}
		feed.Channel.Items = append(feed.Channel.Items, rssItem{
			Title: p.Title, Link: h.postURL(p), GUID: h.postURL(p), Description: desc,
			Author: p.Author, PubDate: pub.UTC().Format(time.RFC1123Z),
		})
	}
	writeXML(w, "application/rss+xml; charset=utf-8", feed)
}

// sitemap lists published posts for search engines (up to 50,000).
//
// Auth: none.
//
// spector:tags feeds
func (h *Handler) sitemap(w http.ResponseWriter, r *http.Request) {
	set := urlSet{NS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: []sitemapURL{{Loc: h.SiteURL + "/", LastMod: time.Now().UTC().Format("2006-01-02")}}}
	for page := 1; page <= 1000; page++ {
		res, err := h.Blog.ListPosts(r.Context(), post.Filter{Paging: domain.NewPaging(page, domain.MaxLimit)})
		if err != nil {
			respond(w, r, 0, nil, err, "")
			return
		}
		for _, p := range res.Items {
			set.URLs = append(set.URLs, sitemapURL{Loc: h.postURL(p), LastMod: p.UpdatedAt.UTC().Format("2006-01-02")})
		}
		if page*res.Limit >= res.Total {
			break
		}
	}
	writeXML(w, "application/xml; charset=utf-8", set)
}

// healthz reports Postgres, Redis and MinIO health.
//
// 503 problem (HANDLER_SERVICE_UNAVAILABLE) with checks when a dependency is down.
// Auth: none.
//
// spector:tags ops
func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	status, down := map[string]string{}, false
	for name, ping := range h.Health {
		if err := ping(r.Context()); err != nil {
			status[name], down = "down", true
		} else {
			status[name] = "ok"
		}
	}
	if down {
		problem(w, r, errorx.New(errorx.ErrHandlerServiceUnavailable, "dependency down").
			WithDetails("a dependency is down").WithExtension("checks", status))
		return
	}
	writeJSON(w, http.StatusOK, status)
}
