package http

import (
	"net/http"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/report"
)

// createReport reports a post, comment or user to moderators.
//
// Body: {"target_type": "post"|"comment"|"user", "target_id": n,
// "reason": "spam"|"abuse"|"harassment"|"off_topic"|"other", "note": "..."}.
// note is required for reason other. One open report per target per caller
// (409 otherwise). Rate limit: 20 per hour.
// Auth: report.create.
//
// spector:tags moderation
func (h *Handler) createReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "report", 20, time.Hour) {
		return
	}
	var in struct {
		TargetType report.TargetType `json:"target_type"`
		TargetID   int               `json:"target_id"`
		Reason     report.Reason     `json:"reason"`
		Note       string            `json:"note"`
	}
	if decode(w, r, &in) {
		rep, err := h.Blog.Report(r.Context(), actor, in.TargetType, in.TargetID, in.Reason, in.Note)
		respond(w, r, http.StatusCreated, rep, err, "target not found")
	}
}

// adminReports lists reports, newest first.
//
// status: open, resolved, dismissed or empty for all. reports is the number
// of open reports on the same target.
// Auth: report.moderate.
//
// spector:tags moderation
func (h *Handler) adminReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); ok {
		p, err := h.Blog.ListReports(r.Context(), report.ReportStatus(r.URL.Query().Get("status")), paging(r))
		respond(w, r, http.StatusOK, p, err, "")
	}
}

// decideReport resolves or dismisses an open report and all other open
// reports on the same target.
//
// Body: {"status": "resolved"|"dismissed", "remove_content": bool}.
// remove_content deletes the reported post or comment. 409 if already decided.
// Auth: report.moderate (+ post.delete / comment.delete to remove).
//
// spector:tags moderation
func (h *Handler) decideReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	var in struct {
		Status        report.ReportStatus `json:"status"`
		RemoveContent bool                `json:"remove_content"`
	}
	if ok && decode(w, r, &in) {
		rep, err := h.Blog.DecideReport(r.Context(), actor, id, in.Status, in.RemoveContent)
		respond(w, r, http.StatusOK, rep, err, "report not found")
	}
}
