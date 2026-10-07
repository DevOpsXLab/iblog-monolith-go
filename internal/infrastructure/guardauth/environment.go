package guardauth

import (
	"context"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // ACCESS_TIMEZONE must resolve in a distroless image too

	"github.com/bakhod1r/errorx"
	"github.com/bakhod1r/guard"
	"github.com/bakhod1r/guard/httpguard"
)

type envKey struct{}

// Environment is the request context ABAC conditions see as env.*:
//
//	env.ip       client IP ("203.0.113.7")
//	env.time     wall clock in the access time zone, "HH:MM" (compare with gte/lt)
//	env.weekday  "mon" … "sun"
//	env.date     "2006-01-02" in the access time zone
//	env.now      RFC3339 UTC
//	env.method   HTTP method
//	env.origin   Origin header, "" for non-browser clients (not proof of the client)
func Environment(r *http.Request, clientIP func(*http.Request) string, loc *time.Location, now time.Time) map[string]any {
	local := now.In(loc)
	return map[string]any{
		"ip":      clientIP(r),
		"time":    local.Format("15:04"),
		"weekday": strings.ToLower(local.Weekday().String()[:3]),
		"date":    local.Format(time.DateOnly),
		"now":     now.UTC().Format(time.RFC3339),
		"method":  r.Method,
		"origin":  r.Header.Get("Origin"),
	}
}

func environment(ctx context.Context) map[string]any {
	env, _ := ctx.Value(envKey{}).(map[string]any)
	return env
}

// AccessGate puts the request's Environment in its context, so every
// Authorizer.Can sees env.*, and refuses a signed-in caller the whole API
// when the "app.access" check fails. Every role that may use the API is
// granted app.access, so only a deny policy (by time, IP, …) closes it.
// Signing out stays open.
func AccessGate(g *guard.Guard, clientIP func(*http.Request) string, loc *time.Location) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := Environment(r, clientIP, loc, time.Now())
			r = r.WithContext(context.WithValue(r.Context(), envKey{}, env))
			p := httpguard.PrincipalFrom(r)
			if p == nil || isSignOut(r) {
				next.ServeHTTP(w, r)
				return
			}
			// An API key's scopes limit what it may do, not whether its owner
			// may use the API now: the gate judges the owner.
			owner := *p
			owner.APIKey = nil
			d, err := g.Authorize(r.Context(), &owner, "access", guard.Resource{Type: "app"}, env)
			if err != nil {
				errorx.WriteProblemRequest(w, r, errorx.New(errorx.ErrInternal, "access gate"))
				return
			}
			if !d.Allowed {
				errorx.WriteProblemRequest(w, r, errorx.New(errorx.ErrForbidden, "access_closed").
					WithDetails("access is closed for this account at this time or from this network"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSignOut(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/auth/logout", "/api/guard/auth/logout", "/api/guard/auth/logout-all":
		return true
	}
	return false
}
