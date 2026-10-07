// Package http is the REST interface over the application layer.
package http

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
	"go.uber.org/zap"
)

// Limiter allows n requests per window for key.
type Limiter func(ctx context.Context, key string, n int, window time.Duration) (ok bool, retryAfter time.Duration)

// Deps are what the handlers need.
type Deps struct {
	Blog     *application.Blog
	Accounts *application.Accounts
	Storage  application.Storage
	Jobs     application.Jobs
	Events   application.Events
	Limit    Limiter
	// UserID returns the authenticated caller's id from the request context.
	UserID func(ctx context.Context) int
	Health map[string]func(context.Context) error
	// SiteURL is the public client site, for RSS and sitemap links.
	SiteURL string
	// ClientIP resolves the caller's address (trusted-proxy aware).
	ClientIP func(*http.Request) string
	// Tickets issues and redeems single-use stream tickets for SSE.
	Tickets application.ResetTokens
	// Analytics records product events (nil disables tracking).
	Analytics *application.Analytics
	// Captcha verifies a signup CAPTCHA token (nil: no CAPTCHA).
	Captcha func(ctx context.Context, token, remoteIP string) error
	// CaptchaSiteKey is the public Turnstile key the client renders with.
	CaptchaSiteKey string
	// SiteName is the product name in prerendered pages and feeds.
	SiteName string
}

type Handler struct{ Deps }

func NewHandler(d Deps) *Handler { return &Handler{Deps: d} }

// Routes registers the blog API. Guard's own routes (/api/guard/...) are
// mounted next to these by the app.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/posts", h.listPosts)
	mux.HandleFunc("GET /api/posts/trending", h.trending)
	mux.HandleFunc("GET /api/slug/{slug}", h.getPostBySlug)
	mux.HandleFunc("GET /api/posts/{id}", h.getPost)
	mux.HandleFunc("POST /api/posts", h.createPost)
	mux.HandleFunc("PUT /api/posts/{id}", h.updatePost)
	mux.HandleFunc("DELETE /api/posts/{id}", h.deletePost)
	mux.HandleFunc("POST /api/posts/{id}/view", h.viewPost)
	mux.HandleFunc("POST /api/posts/{id}/read", h.readPost)
	mux.HandleFunc("POST /api/posts/import", h.importPost)
	mux.HandleFunc("POST /api/posts/{id}/like", h.likePost)
	mux.HandleFunc("DELETE /api/posts/{id}/like", h.unlikePost)
	mux.HandleFunc("POST /api/posts/{id}/clap", h.clapPost)
	mux.HandleFunc("GET /api/posts/{id}/highlights", h.listHighlights)
	mux.HandleFunc("POST /api/posts/{id}/highlights", h.addHighlight)
	mux.HandleFunc("DELETE /api/highlights/{id}", h.deleteHighlight)
	mux.HandleFunc("POST /api/posts/{id}/bookmark", h.bookmark)
	mux.HandleFunc("DELETE /api/posts/{id}/bookmark", h.unbookmark)
	mux.HandleFunc("GET /api/posts/{id}/related", h.relatedPosts)
	mux.HandleFunc("GET /api/posts/{id}/series", h.postSeries)
	mux.HandleFunc("GET /api/posts/{id}/revisions", h.listRevisions)
	mux.HandleFunc("GET /api/posts/{id}/revisions/{version}", h.getRevision)
	mux.HandleFunc("POST /api/posts/{id}/revisions/{version}/restore", h.restoreRevision)

	mux.HandleFunc("POST /api/series", h.createSeries)
	mux.HandleFunc("GET /api/series/{slug}", h.getSeries)
	mux.HandleFunc("PATCH /api/series/{slug}", h.updateSeries)
	mux.HandleFunc("DELETE /api/series/{slug}", h.deleteSeries)
	mux.HandleFunc("PUT /api/series/{slug}/posts", h.setSeriesPosts)

	mux.HandleFunc("POST /api/lists", h.createList)
	mux.HandleFunc("GET /api/lists/{slug}", h.getList)
	mux.HandleFunc("PATCH /api/lists/{slug}", h.updateList)
	mux.HandleFunc("DELETE /api/lists/{slug}", h.deleteList)
	mux.HandleFunc("GET /api/lists/{slug}/posts", h.listPostsOf)
	mux.HandleFunc("PUT /api/lists/{slug}/posts/{id}", h.addToList)
	mux.HandleFunc("DELETE /api/lists/{slug}/posts/{id}", h.removeFromList)
	mux.HandleFunc("GET /api/me/lists", h.myLists)
	mux.HandleFunc("GET /api/users/{username}/lists", h.userLists)

	mux.HandleFunc("GET /api/posts/{id}/comments", h.listComments)
	mux.HandleFunc("GET /api/posts/{id}/comments/stream", h.commentStream)
	mux.HandleFunc("POST /api/posts/{id}/comments", h.addComment)
	mux.HandleFunc("PUT /api/comments/{id}", h.editComment)
	mux.HandleFunc("DELETE /api/comments/{id}", h.deleteComment)
	mux.HandleFunc("POST /api/comments/{id}/like", h.likeComment)
	mux.HandleFunc("DELETE /api/comments/{id}/like", h.unlikeComment)

	mux.HandleFunc("GET /api/categories", h.listCategories)
	mux.HandleFunc("POST /api/categories", h.createCategory)
	mux.HandleFunc("DELETE /api/categories/{id}", h.deleteCategory)
	mux.HandleFunc("GET /api/categories/{id}/posts", h.categoryPosts)
	mux.HandleFunc("GET /api/labels", h.listLabels)
	mux.HandleFunc("POST /api/labels", h.createLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", h.deleteLabel)
	mux.HandleFunc("GET /api/tags", h.listTags)
	mux.HandleFunc("POST /api/tags/{tag}/follow", h.followTag)
	mux.HandleFunc("DELETE /api/tags/{tag}/follow", h.unfollowTag)
	mux.HandleFunc("GET /api/me/tags", h.myTags)

	mux.HandleFunc("POST /api/auth/register", h.register)
	mux.HandleFunc("POST /api/auth/login", h.login)
	mux.HandleFunc("POST /api/auth/login/2fa", h.loginMFA)
	mux.HandleFunc("POST /api/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/auth/logout", h.logout)
	mux.HandleFunc("POST /api/auth/logout-all", h.logoutAll)
	mux.HandleFunc("POST /api/auth/forgot-password", h.forgotPassword)
	mux.HandleFunc("POST /api/auth/reset-password", h.resetPassword)
	mux.HandleFunc("POST /api/auth/verify-email", h.verifyEmail)
	mux.HandleFunc("POST /api/me/verify-email", h.resendVerification)

	mux.HandleFunc("GET /api/me", h.me)
	mux.HandleFunc("PATCH /api/me", h.updateMe)
	mux.HandleFunc("DELETE /api/me", h.deleteMe)
	mux.HandleFunc("PUT /api/me/password", h.changePassword)
	mux.HandleFunc("GET /api/me/posts", h.myPosts)
	mux.HandleFunc("GET /api/me/feed", h.feed)
	mux.HandleFunc("GET /api/me/stats", h.myStats)
	mux.HandleFunc("GET /api/me/stats/{id}", h.postDailyStats)
	mux.HandleFunc("GET /api/me/for-you", h.forYou)
	mux.HandleFunc("GET /api/me/hidden", h.hidden)
	mux.HandleFunc("POST /api/me/hidden", h.hide)
	mux.HandleFunc("DELETE /api/me/hidden/{kind}/{target}", h.unhide)
	mux.HandleFunc("PUT /api/me/pin", h.pin)
	mux.HandleFunc("DELETE /api/me/pin", h.unpin)
	mux.HandleFunc("GET /api/me/bookmarks", h.myBookmarks)
	mux.HandleFunc("GET /api/me/notifications", h.notifications)
	mux.HandleFunc("POST /api/me/notifications/read", h.readNotifications)
	mux.HandleFunc("GET /api/me/notifications/stream", h.notificationStream)
	mux.HandleFunc("POST /api/me/stream-ticket", h.streamTicket)
	mux.HandleFunc("GET /api/me/sessions", h.mySessions)
	mux.HandleFunc("DELETE /api/me/sessions/{id}", h.revokeSession)
	mux.HandleFunc("GET /api/me/2fa", h.mfaStatus)
	mux.HandleFunc("POST /api/me/2fa/setup", h.mfaSetup)
	mux.HandleFunc("POST /api/me/2fa/enable", h.mfaEnable)
	mux.HandleFunc("POST /api/me/2fa/disable", h.mfaDisable)
	mux.HandleFunc("POST /api/me/2fa/backup-codes", h.mfaBackupCodes)
	mux.HandleFunc("GET /api/me/blocks", h.myBlocks)
	mux.HandleFunc("GET /api/me/mutes", h.myMutes)
	mux.HandleFunc("GET /api/me/suggestions/users", h.suggestedUsers)

	mux.HandleFunc("GET /api/users/{username}", h.profile)
	mux.HandleFunc("GET /api/users/{username}/posts", h.userPosts)
	mux.HandleFunc("GET /api/users/{username}/followers", h.followers)
	mux.HandleFunc("GET /api/users/{username}/following", h.following)
	mux.HandleFunc("POST /api/users/{username}/follow", h.follow)
	mux.HandleFunc("DELETE /api/users/{username}/follow", h.unfollow)
	mux.HandleFunc("POST /api/users/{username}/subscribe", h.subscribe)
	mux.HandleFunc("DELETE /api/users/{username}/subscribe", h.unsubscribe)
	mux.HandleFunc("POST /api/unsubscribe", h.unsubscribeLink)
	mux.HandleFunc("POST /api/users/{username}/block", h.block)
	mux.HandleFunc("DELETE /api/users/{username}/block", h.unblock)
	mux.HandleFunc("POST /api/users/{username}/mute", h.mute)
	mux.HandleFunc("DELETE /api/users/{username}/mute", h.unmute)
	mux.HandleFunc("GET /api/users/{username}/series", h.userSeries)

	mux.HandleFunc("GET /api/admin/posts", h.adminPosts)
	mux.HandleFunc("GET /api/admin/users", h.adminUsers)
	mux.HandleFunc("GET /api/admin/bans", h.listBans)
	mux.HandleFunc("GET /api/admin/users/{username}/ban", h.getBan)
	mux.HandleFunc("PUT /api/admin/users/{username}/ban", h.banUser)
	mux.HandleFunc("DELETE /api/admin/users/{username}/ban", h.unbanUser)
	mux.HandleFunc("GET /api/admin/comments", h.adminComments)
	mux.HandleFunc("GET /api/admin/stats", h.stats)
	mux.HandleFunc("GET /api/admin/reports", h.adminReports)
	mux.HandleFunc("PUT /api/admin/reports/{id}", h.decideReport)
	mux.HandleFunc("POST /api/reports", h.createReport)

	mux.HandleFunc("POST /api/publications", h.createPublication)
	mux.HandleFunc("GET /api/publications/{slug}", h.getPublication)
	mux.HandleFunc("PATCH /api/publications/{slug}", h.updatePublication)
	mux.HandleFunc("DELETE /api/publications/{slug}", h.deletePublication)
	mux.HandleFunc("GET /api/publications/{slug}/posts", h.publicationPosts)
	mux.HandleFunc("GET /api/publications/{slug}/members", h.publicationMembers)
	mux.HandleFunc("PUT /api/publications/{slug}/members/{username}", h.setPublicationMember)
	mux.HandleFunc("DELETE /api/publications/{slug}/members/{username}", h.removePublicationMember)
	mux.HandleFunc("GET /api/me/publications", h.myPublications)

	mux.HandleFunc("POST /api/uploads", h.upload)
	mux.HandleFunc("GET /api/uploads/{key...}", h.download)

	mux.HandleFunc("GET /api/feed.xml", h.rss)
	mux.HandleFunc("GET /api/users/{username}/feed.xml", h.userRSS)
	mux.HandleFunc("GET /api/sitemap.xml", h.sitemap)
	mux.HandleFunc("GET /api/healthz", h.healthz)
	mux.HandleFunc("GET /api/config", h.publicConfig)
	mux.HandleFunc("POST /api/events", h.trackEvent)
	mux.HandleFunc("GET /api/admin/analytics", h.analytics)
	mux.HandleFunc("GET /api/prerender/{path...}", h.prerender)
	// Anything else under /api is a JSON 404, not the mux's plain text.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "no such endpoint")
	})
}

// Caller helpers

// viewerID is the logged-in caller's id, or 0.
func (h *Handler) viewerID(r *http.Request) int { return h.UserID(r.Context()) }

// requireUser writes 401 unless the request carries a valid session.
func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (user.User, bool) {
	id := h.viewerID(r)
	if id == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return user.User{}, false
	}
	u, err := h.Accounts.User(r.Context(), id)
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return u, false
	}
	return u, true
}

// allow applies a rate limit keyed by user (or IP for anonymous callers)
// and writes 429 when exceeded.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, name string, n int, window time.Duration) bool {
	key := "ip:" + h.clientIP(r)
	if id := h.viewerID(r); id != 0 {
		key = "user:" + strconv.Itoa(id)
	}
	ok, retry := h.Limit(r.Context(), name+":"+key, n, window)
	if !ok {
		tooManyRequests(w, r, retry)
	}
	return ok
}

func (h *Handler) clientIP(r *http.Request) string {
	if h.ClientIP != nil {
		return h.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) meta(r *http.Request) application.Meta {
	return application.Meta{IP: h.clientIP(r), UserAgent: r.UserAgent()}
}

func bearer(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok
}

// Request helpers

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeError(w, r, http.StatusBadRequest, "bad id")
		return 0, false
	}
	return id, true
}

func paging(r *http.Request) domain.Paging {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	p := domain.NewPaging(page, limit)
	p.Cursor = r.URL.Query().Get("cursor")
	return p
}

const maxJSONBody = 1 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(v); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

// Response helpers

// envelope is every JSON success body: the resource or list in data, page
// metadata in meta for paginated lists.
type envelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	body := envelope{Data: v}
	if p, ok := v.(domain.Paginated); ok {
		body.Data, body.Meta = p.Paginated()
	}
	if rv := reflect.ValueOf(body.Data); rv.Kind() == reflect.Slice && rv.IsNil() {
		body.Data = []any{} // none is [], not null
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		zap.L().Warn("write json", zap.Error(err))
	}
}

// respond writes v, or err as problem details. notFound is the 404 detail.
func respond(w http.ResponseWriter, r *http.Request, code int, v any, err error, notFound string) {
	if err != nil {
		problem(w, r, toProblem(err, notFound))
		return
	}
	writeJSON(w, code, v)
}

func noContent(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	if err != nil {
		problem(w, r, toProblem(err, notFound))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
