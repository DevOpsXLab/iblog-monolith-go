package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/bakhod1r/errorx"
)

// RequestIDHeader carries the request's correlation id both ways.
const RequestIDHeader = "X-Request-ID"

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// RequestID keeps a well-formed incoming X-Request-ID or makes one, echoes it
// in the response and puts it in the context, so problem responses carry it
// as request_id and logs can name the same request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !requestIDRe.MatchString(id) {
			b := make([]byte, 16)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), errorx.ContextKeyRequestID, id)))
	})
}

// RequestIDFrom returns the request id RequestID stored.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(errorx.ContextKeyRequestID).(string)
	return id
}
