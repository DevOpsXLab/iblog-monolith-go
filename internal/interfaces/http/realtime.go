package http

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type shutdownKey struct{}

// WithShutdown returns parent carrying done, which is cancelled when the
// server starts shutting down. Use it as http.Server.BaseContext so
// long-lived SSE streams end and Shutdown does not wait for them, while
// ordinary in-flight requests keep their own context and finish.
func WithShutdown(parent, done context.Context) context.Context {
	return context.WithValue(parent, shutdownKey{}, done)
}

// shuttingDown is closed once the server is shutting down (never if unset).
func shuttingDown(ctx context.Context) <-chan struct{} {
	if done, ok := ctx.Value(shutdownKey{}).(context.Context); ok {
		return done.Done()
	}
	return nil
}

type readInput struct {
	IDs []int64 `json:"ids"`
	All bool    `json:"all"` // mark every notification read; ids are ignored
}

// notifications lists the caller's notifications, newest first, with the
// unread count.
//
// Types: comment, reply, like, follow, new_post.
// Auth: user.
//
// spector:tags notifications
func (h *Handler) notifications(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		n, err := h.Blog.Notifications(r.Context(), actor, paging(r))
		respond(w, r, http.StatusOK, n, err, "")
	}
}

// readNotifications marks notifications as read.
//
// Auth: user.
//
// spector:tags notifications
func (h *Handler) readNotifications(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in readInput
	if ok && decode(w, r, &in) {
		noContent(w, r, h.Blog.MarkNotificationsRead(r.Context(), actor, in.IDs, in.All), "")
	}
}

// notificationStream streams new notifications (Server-Sent Events).
//
// EventSource cannot send headers, so browsers pass a single-use ticket
// from POST /api/me/stream-ticket as ?ticket= (never the session token,
// which would end up in access logs).
// Auth: user.
//
// spector:tags notifications
func (h *Handler) notificationStream(w http.ResponseWriter, r *http.Request) {
	id := h.viewerID(r)
	if id == 0 && h.Tickets != nil {
		if t := r.URL.Query().Get("ticket"); t != "" {
			if uid, err := h.Tickets.Consume(r.Context(), t); err == nil {
				id = uid
			}
		}
	}
	if id == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.stream(w, r, application.NotificationsChannel(id))
}

type streamTicket struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"` // seconds
}

const streamTicketTTL = 30 * time.Second

// streamTicket issues a single-use ticket (valid 30 s) for
// GET /api/me/notifications/stream?ticket=.
//
// Auth: user.
//
// spector:tags notifications
func (h *Handler) streamTicket(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	t, err := h.Tickets.Issue(r.Context(), actor.ID, streamTicketTTL)
	respond(w, r, http.StatusCreated, streamTicket{Ticket: t, ExpiresIn: int(streamTicketTTL.Seconds())}, err, "")
}

const keepAlive = 25 * time.Second

// stream relays a pub/sub channel as Server-Sent Events until the client leaves.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request, channel string) {
	rc := http.NewResponseController(w)
	// The server's WriteTimeout would cut a long-lived stream.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	msgs, err := h.Events.Subscribe(r.Context(), channel)
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	rc.Flush()

	span := trace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("sse.channel", channel))
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	stopping := shuttingDown(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case <-stopping: // server shutdown: end the stream; clients reconnect
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case m, ok := <-msgs:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: {\"data\":%s}\n\n", m) // same envelope as JSON responses
			span.AddEvent("sse.message", trace.WithAttributes(attribute.Int("size", len(m))))
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
