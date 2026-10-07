package application

import (
	"context"
	"errors"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/report"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
)

// Report files a report on a post, comment or user the caller can see.
func (b *Blog) Report(ctx context.Context, actor user.User, t report.TargetType, targetID int, reason report.Reason, note string) (report.Report, error) {
	ctx, span := tracer.Start(ctx, "Blog.Report")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "create", 0, 0); err != nil {
		return report.Report{}, err
	}
	r, err := report.New(actor.ID, t, targetID, reason, note)
	if err != nil {
		return r, err
	}
	if err := b.reportTarget(ctx, actor, r); err != nil {
		return r, err
	}
	return b.Reports.Create(ctx, r)
}

// reportTarget checks that the target exists and is visible to actor and
// that actor is not reporting their own content.
func (b *Blog) reportTarget(ctx context.Context, actor user.User, r report.Report) error {
	ownerID := 0
	switch r.TargetType {
	case report.TargetPost:
		p, err := b.readable(ctx, r.TargetID, actor.ID)
		if err != nil {
			return err
		}
		ownerID = p.UserID
	case report.TargetComment:
		c, err := b.Comments.Get(ctx, r.TargetID)
		if err != nil {
			return err
		}
		if _, err := b.readable(ctx, c.PostID, actor.ID); err != nil {
			return err
		}
		ownerID = c.UserID
	case report.TargetUser:
		u, err := b.Users.GetByID(ctx, r.TargetID)
		if err != nil {
			return err
		}
		ownerID = u.ID
	}
	if ownerID == actor.ID {
		return domain.Invalid("cannot report your own content")
	}
	return nil
}

// ListReports lists reports for moderators; status empty means all.
func (b *Blog) ListReports(ctx context.Context, status report.ReportStatus, p domain.Paging) (report.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ListReports")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return report.Page{}, err
	}
	return b.Reports.List(ctx, status, p)
}

// DecideReport resolves or dismisses a report and every other open report
// on the same target. With remove, a reported post or comment is deleted
// first (needs post.delete / comment.delete).
func (b *Blog) DecideReport(ctx context.Context, actor user.User, id int, s report.ReportStatus, remove bool) (report.Report, error) {
	ctx, span := tracer.Start(ctx, "Blog.DecideReport")
	defer span.End()
	if err := b.Authz.Can(ctx, "report", "moderate", 0, 0); err != nil {
		return report.Report{}, err
	}
	if err := report.ValidateDecision(s); err != nil {
		return report.Report{}, err
	}
	r, err := b.Reports.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status != report.StatusOpen {
		return r, domain.ErrConflict
	}
	if remove {
		if s != report.StatusResolved {
			return r, domain.Invalid("remove_content needs status resolved")
		}
		if err := b.removeReported(ctx, actor, r); err != nil {
			return r, err
		}
	}
	r, err = b.Reports.Decide(ctx, id, s, actor.ID)
	if err == nil {
		b.Audit.Record(ctx, actor.ID, "report."+string(s), "report:"+itoa(id),
			map[string]any{"target": string(r.TargetType) + ":" + itoa(r.TargetID), "removed": remove})
	}
	return r, err
}

func (b *Blog) removeReported(ctx context.Context, actor user.User, r report.Report) error {
	var err error
	switch r.TargetType {
	case report.TargetPost:
		err = b.DeletePost(ctx, actor, r.TargetID)
	case report.TargetComment:
		err = b.DeleteComment(ctx, actor, r.TargetID)
	default:
		return domain.Invalid("remove_content works for posts and comments")
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil // already gone
	}
	return err
}
