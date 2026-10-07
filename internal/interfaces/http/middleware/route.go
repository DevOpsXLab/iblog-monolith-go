package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// SpanRoute names the request span after the route matched by next (a
// ServeMux, which sets r.Pattern on the request it serves), e.g.
// "GET /api/posts/{id}". Wrap the mux directly so r is the one it mutates.
func SpanRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		setRoute(r)
		if r.Pattern == "" {
			return
		}
		span := trace.SpanFromContext(r.Context())
		span.SetName(r.Pattern)
		span.SetAttributes(attribute.String("http.route", r.Pattern))
	})
}
