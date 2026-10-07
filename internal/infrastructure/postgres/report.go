package postgres

import (
	"context"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/report"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReportRepo struct{ db *pgxpool.Pool }

func NewReportRepo(db *pgxpool.Pool) *ReportRepo { return &ReportRepo{db: db} }

const reportCols = `r.id, r.reporter_id, COALESCE(u.username, ''), r.target_type, r.target_id, r.reason, r.note,
	r.status, COALESCE(r.resolved_by, 0), r.resolved_at, r.created_at,
	(SELECT count(*) FROM reports o WHERE o.target_type = r.target_type AND o.target_id = r.target_id AND o.status = 'open')`

const reportFrom = ` FROM reports r LEFT JOIN users u ON u.id = r.reporter_id`

func scanReport(row pgx.Row, extra ...any) (report.Report, error) {
	var r report.Report
	dest := append([]any{&r.ID, &r.ReporterID, &r.Reporter, &r.TargetType, &r.TargetID, &r.Reason, &r.Note,
		&r.Status, &r.ResolvedBy, &r.ResolvedAt, &r.CreatedAt, &r.Reports}, extra...)
	return r, notFound(row.Scan(dest...))
}

func (r *ReportRepo) Create(ctx context.Context, rep report.Report) (report.Report, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO reports (reporter_id, target_type, target_id, reason, note)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		rep.ReporterID, rep.TargetType, rep.TargetID, rep.Reason, rep.Note).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return rep, domain.ErrConflict
	}
	if err != nil {
		return rep, err
	}
	return r.Get(ctx, id)
}

func (r *ReportRepo) Get(ctx context.Context, id int) (report.Report, error) {
	return scanReport(r.db.QueryRow(ctx, `SELECT `+reportCols+reportFrom+` WHERE r.id = $1`, id))
}

func (r *ReportRepo) List(ctx context.Context, status report.ReportStatus, p domain.Paging) (report.Page, error) {
	page := report.Page{Items: []report.Report{}, Page: p.Page, Limit: p.Limit}
	rows, err := r.db.Query(ctx, `SELECT `+reportCols+`, count(*) OVER ()`+reportFrom+`
		WHERE ($1 = '' OR r.status = $1) ORDER BY r.id DESC LIMIT $2 OFFSET $3`, status, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		rep, err := scanReport(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, rep)
	}
	return page, rows.Err()
}

func (r *ReportRepo) Decide(ctx context.Context, id int, s report.ReportStatus, moderatorID int) (report.Report, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var t report.TargetType
		var targetID int
		var status report.ReportStatus
		err := tx.QueryRow(ctx, `SELECT target_type, target_id, status FROM reports WHERE id = $1 FOR UPDATE`, id).
			Scan(&t, &targetID, &status)
		if err != nil {
			return notFound(err)
		}
		if status != report.StatusOpen {
			return domain.ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE reports SET status = $3, resolved_by = $4, resolved_at = now()
			WHERE target_type = $1 AND target_id = $2 AND status = 'open'`, t, targetID, s, moderatorID)
		return err
	})
	if err != nil {
		return report.Report{}, err
	}
	return r.Get(ctx, id)
}
