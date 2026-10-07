package middleware

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/guardauth"
)

const (
	cacheTTL    = 5 * time.Minute
	cacheVerKey = "cache:ver"
)

// Paths whose GET responses are never cached.
var noCachePrefixes = []string{"/api/uploads/", "/api/healthz", "/api/me", "/api/admin", "/api/guard", "/api/auth"}

// Cache serves anonymous GET JSON responses from Redis. Every successful
// write bumps cache:ver, so all older keys stop being read and expire by TTL.
func Cache(rdb *redis.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		switch r.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			flag := new(bool)
			r = r.WithContext(context.WithValue(ctx, skipInvalidateKey{}, flag))
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if rec.status < 400 && !*flag && !sessionOnly(r.URL.Path) {
				if err := rdb.Incr(ctx, cacheVerKey).Err(); err != nil {
					zap.L().Warn("cache invalidate", zap.Any("ctx", ctx), zap.Error(err))
				}
			}
			return
		default: // OPTIONS, TRACE, ...: neither cached nor invalidating
			next.ServeHTTP(w, r)
			return
		}

		// Per-user responses must never be shared through the cache.
		if authenticated(r) || !cacheable(r.URL.Path) || !knownQuery(r) {
			next.ServeHTTP(w, r)
			return
		}

		ver, _ := rdb.Get(ctx, cacheVerKey).Int64()
		key := "cache:" + strconv.FormatInt(ver, 10) + ":" + r.Method + ":" + cacheKeyURI(r)
		if body, err := rdb.Get(ctx, key).Bytes(); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.Write(body)
			return
		}

		w.Header().Set("X-Cache", "MISS")
		rec := &recorder{ResponseWriter: w, status: http.StatusOK, buffer: true}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusOK && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			if err := rdb.Set(ctx, key, rec.buf.Bytes(), cacheTTL).Err(); err != nil {
				zap.L().Warn("cache set", zap.Any("ctx", ctx), zap.Error(err))
			}
		}
	})
}

// authenticated reports whether the response may depend on the caller:
// a resolved principal, or any credential the auth layer may accept.
func authenticated(r *http.Request) bool {
	return guardauth.UserID(r.Context()) != 0 ||
		r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" || hasSessionCookie(r)
}

type skipInvalidateKey struct{}

// SkipInvalidate marks a successful write as not affecting cached responses
// (e.g. view/read counters), so it does not bump cache:ver. No-op outside Cache.
func SkipInvalidate(ctx context.Context) {
	if f, ok := ctx.Value(skipInvalidateKey{}).(*bool); ok {
		*f = true
	}
}

func hasSessionCookie(r *http.Request) bool {
	_, err := r.Cookie("guard_session")
	return err == nil
}

func cacheable(path string) bool {
	if strings.HasSuffix(path, "/stream") {
		return false
	}
	for _, p := range noCachePrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	return true
}

// ResetCache invalidates every cached response. Call on startup so a new
// release never serves bodies cached by the previous one.
func ResetCache(ctx context.Context, rdb *redis.Client) error {
	return rdb.Incr(ctx, cacheVerKey).Err()
}

type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool // headers sent
	buffer bool
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	if r.buffer {
		r.buf.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// sessionOnly paths change sessions or credentials, never public content,
// so they must not throw the shared cache away.
func sessionOnly(path string) bool {
	return strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/guard/")
}

// cacheKeyURI is the path plus the query in canonical (sorted) order, so
// ?a=1&b=2 and ?b=2&a=1 share one entry.
func cacheKeyURI(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.Query().Encode()
}

// cacheQueryParams are every query parameter the public GET handlers read.
// A request with any other parameter is served uncached: random junk in the
// query cannot fill Redis, and no parameter is ever silently ignored in the
// key. Add new parameters here when a handler starts reading one.
var cacheQueryParams = map[string]bool{
	"category": true, "cursor": true, "days": true, "label": true, "limit": true,
	"page": true, "post_id": true, "q": true, "status": true, "tag": true,
}

func knownQuery(r *http.Request) bool {
	for k := range r.URL.Query() {
		if !cacheQueryParams[k] {
			return false
		}
	}
	return true
}
