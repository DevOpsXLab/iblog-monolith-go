// Package report holds content reports for moderation.
package report

import (
	"context"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type TargetType string

const (
	TargetPost    TargetType = "post"
	TargetComment TargetType = "comment"
	TargetUser    TargetType = "user"
)

type Reason string

const (
	ReasonSpam       Reason = "spam"
	ReasonAbuse      Reason = "abuse"
	ReasonHarassment Reason = "harassment"
	ReasonOffTopic   Reason = "off_topic"
	ReasonOther      Reason = "other"
)

type ReportStatus string

const (
	StatusOpen      ReportStatus = "open"
	StatusResolved  ReportStatus = "resolved"  // action taken
	StatusDismissed ReportStatus = "dismissed" // no action needed
)

type Report struct {
	ID         int          `json:"id"`
	ReporterID int          `json:"reporter_id"`
	Reporter   string       `json:"reporter"`
	TargetType TargetType   `json:"target_type"`
	TargetID   int          `json:"target_id"`
	Reason     Reason       `json:"reason"`
	Note       string       `json:"note"`
	Status     ReportStatus `json:"status"`
	ResolvedBy int          `json:"resolved_by"`
	ResolvedAt *time.Time   `json:"resolved_at,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	// Reports is how many open reports the same target has.
	Reports int `json:"reports"`
}

type Page struct {
	Items []Report `json:"items"`
	Total int      `json:"total"`
	Page  int      `json:"page"`
	Limit int      `json:"limit"`
}

const maxNote = 1000

// New validates input and builds an unsaved open report.
func New(reporterID int, t TargetType, targetID int, reason Reason, note string) (Report, error) {
	switch t {
	case TargetPost, TargetComment, TargetUser:
	default:
		return Report{}, domain.Invalid("target_type: post, comment or user")
	}
	switch reason {
	case ReasonSpam, ReasonAbuse, ReasonHarassment, ReasonOffTopic, ReasonOther:
	default:
		return Report{}, domain.Invalid("reason: spam, abuse, harassment, off_topic or other")
	}
	note = strings.TrimSpace(note)
	switch {
	case targetID <= 0:
		return Report{}, domain.Invalid("bad target_id")
	case len(note) > maxNote:
		return Report{}, domain.Invalid("note too long")
	case reason == ReasonOther && note == "":
		return Report{}, domain.Invalid("note required for reason other")
	}
	return Report{ReporterID: reporterID, TargetType: t, TargetID: targetID, Reason: reason, Note: note, Status: StatusOpen}, nil
}

// ValidateDecision checks a moderator's decision on an open report.
func ValidateDecision(s ReportStatus) error {
	if s != StatusResolved && s != StatusDismissed {
		return domain.Invalid("status: resolved or dismissed")
	}
	return nil
}

// Repository returns domain.ErrConflict when the reporter already has an
// open report on the target and domain.ErrNotFound for a missing report.
type Repository interface {
	Create(ctx context.Context, r Report) (Report, error)
	Get(ctx context.Context, id int) (Report, error)
	// List filters by status (empty = all), newest first.
	List(ctx context.Context, status ReportStatus, p domain.Paging) (Page, error)
	// Decide closes every open report on the same target as id, so one
	// decision clears duplicates. It returns domain.ErrConflict when id is
	// not open.
	Decide(ctx context.Context, id int, s ReportStatus, moderatorID int) (Report, error)
}

func (p Page) Paginated() (any, any) { return p.Items, domain.NewMeta(p.Page, p.Limit, p.Total, "") }
