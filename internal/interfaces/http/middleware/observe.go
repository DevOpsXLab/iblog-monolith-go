package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bakhod1r/errorx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by route and status.",
	}, []string{"method", "route", "status"})
	duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// Observe logs each request with zap, records Prometheus metrics and
// recovers from panics. Route labels use the matched mux pattern to keep
// cardinality low.
func Observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		rh := &routeHolder{}
		r = r.WithContext(context.WithValue(r.Context(), routeKey{}, rh))
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // net/http's sentinel: abort silently, never log or answer
				}
				zap.L().Error("panic", zap.Any("ctx", r.Context()), zap.Any("panic", v), zap.String("path", r.URL.Path))
				span := trace.SpanFromContext(r.Context())
				span.RecordError(fmt.Errorf("panic: %v", v), trace.WithStackTrace(true))
				span.SetStatus(codes.Error, "panic")
				if !rec.wrote {
					errorx.WriteProblemRequest(rec, r, errorx.New(errorx.ErrInternal, "panic"))
				}
				rec.status = http.StatusInternalServerError
			}
			// r.Pattern is only set on the request the inner mux serves; the
			// mux copy reports it back through the holder (see SpanRoute).
			route := rh.pattern
			if route == "" {
				route = r.Pattern
			}
			if route == "" {
				route = "unmatched"
			}
			elapsed := time.Since(start)
			requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
			duration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
			zap.L().Info("request", zap.Any("ctx", r.Context()), zap.String("method", r.Method), zap.String("path", r.URL.Path), zap.Int("status", rec.status),
				zap.String("request_id", RequestIDFrom(r.Context())), zap.Int64("duration_ms", elapsed.Milliseconds()))
		}()
		next.ServeHTTP(rec, r)
	})
}

type routeKey struct{}

// routeHolder carries the matched mux pattern from the inner mux back out
// to Observe, whose own request copy never sees r.Pattern.
type routeHolder struct{ pattern string }

// setRoute records the matched pattern for Observe's metrics.
func setRoute(r *http.Request) {
	if h, ok := r.Context().Value(routeKey{}).(*routeHolder); ok && r.Pattern != "" {
		h.pattern = r.Pattern
	}
}
