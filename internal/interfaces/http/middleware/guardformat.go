package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bakhod1r/errorx"
)

// guardContentType is what Guard's httpguard writes; the blog API writes
// plain "application/json", so only Guard's responses match.
const guardContentType = "application/json; charset=utf-8"

// GuardFormat rewrites Guard's JSON responses (its /api/guard routes and the
// authentication middleware's rejections) into the API's own format:
// successes become {"data": ...} and Guard's {"error": {code, message}}
// becomes RFC 9457 problem details. Everything else streams through.
func GuardFormat(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := &guardWriter{ResponseWriter: w}
		next.ServeHTTP(gw, r)
		if gw.capture {
			translateGuard(w, r, gw.status, gw.buf.Bytes())
		}
	})
}

type guardWriter struct {
	http.ResponseWriter
	decided, capture bool
	status           int
	buf              bytes.Buffer
}

func (g *guardWriter) WriteHeader(code int) {
	if g.decided {
		return
	}
	g.decided, g.status = true, code
	if g.Header().Get("Content-Type") == guardContentType {
		g.capture = true
		return
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *guardWriter) Write(b []byte) (int, error) {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.capture {
		return g.buf.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *guardWriter) Flush() {
	if !g.capture {
		http.NewResponseController(g.ResponseWriter).Flush()
	}
}

func (g *guardWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// guardLists are Guard's list responses, {"<name>": [...]}: their array
// becomes data.
var guardLists = map[string]bool{
	"sessions": true, "api_keys": true, "grants": true, "roles": true,
	"permissions": true, "policies": true, "events": true,
}

func translateGuard(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	w.Header().Del("Content-Type")
	if status >= 400 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.Unmarshal(body, &e)
		errorx.WriteProblemRequest(w, r, guardProblem(status, e.Error.Code, e.Error.Message))
		return
	}
	var obj map[string]json.RawMessage
	data := json.RawMessage(bytes.TrimSpace(body))
	if json.Unmarshal(body, &obj) == nil && len(obj) == 1 {
		for k, v := range obj {
			if guardLists[k] {
				data = v
			}
		}
	}
	if len(data) == 0 || string(data) == "null" {
		data = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	w.Write([]byte(`{"data":`))
	w.Write(data)
	w.Write([]byte("}\n"))
}

// guardProblem maps a Guard error code to the API's error codes; Guard's
// own code stays in the reason member, its message in detail.
func guardProblem(status int, code, msg string) *errorx.AppError {
	var c string
	switch {
	case code == "invalid_credentials":
		c = "INVALID_CREDENTIALS"
	case code == "unauthenticated":
		c = errorx.ErrUnauthorized
	case code == "rate_limited" || code == "account_locked":
		c = errorx.ErrHandlerTooManyRequests
	case code == "internal":
		c, msg = errorx.ErrInternal, ""
	case strings.HasSuffix(code, "_not_found"):
		c = errorx.ErrNotFound
	case status == http.StatusConflict:
		c = errorx.ErrConflict
	case status == http.StatusForbidden:
		c = errorx.ErrForbidden
	case code == "invalid_body" || code == "body_too_large":
		c = errorx.ErrBadRequest
	case status == http.StatusBadRequest:
		c = errorx.ErrValidation
	default:
		c = errorx.ErrInternal
	}
	e := errorx.New(c, "guard: "+code).WithDetails(msg)
	e.HTTPStatus = status // keep Guard's status (e.g. 413, 423)
	if code != "" && code != "internal" {
		e.WithExtension("reason", code)
	}
	return e
}
