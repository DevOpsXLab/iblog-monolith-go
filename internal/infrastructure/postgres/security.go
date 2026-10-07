package postgres

import (
	"context"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/mfa"
	"github.com/iBlog/iblog-monolith-go/internal/domain/relation"
	"github.com/iBlog/iblog-monolith-go/internal/domain/sanction"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MFARepo implements mfa.Repository.
type MFARepo struct{ db *pgxpool.Pool }

func NewMFARepo(db *pgxpool.Pool) *MFARepo { return &MFARepo{db: db} }

func (r *MFARepo) Get(ctx context.Context, userID int) (mfa.State, error) {
	var s mfa.State
	err := r.db.QueryRow(ctx, `SELECT secret, enabled_at, last_step FROM user_mfa WHERE user_id = $1`, userID).
		Scan(&s.Secret, &s.EnabledAt, &s.LastStep)
	return s, notFound(err)
}

func (r *MFARepo) SavePending(ctx context.Context, userID int, secret string) error {
	tag, err := r.db.Exec(ctx, `INSERT INTO user_mfa (user_id, secret) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, last_step = 0, created_at = now()
		WHERE user_mfa.enabled_at IS NULL`, userID, secret)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return mfa.ErrEnabled
	}
	return nil
}

func (r *MFARepo) Enable(ctx context.Context, userID int, step int64, hashes []string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE user_mfa SET enabled_at = now(), last_step = $2
			WHERE user_id = $1 AND enabled_at IS NULL`, userID, step)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return mfa.ErrNoSetup
		}
		return replaceCodes(ctx, tx, userID, hashes)
	})
}

func (r *MFARepo) Disable(ctx context.Context, userID int) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_backup_codes WHERE user_id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, userID)
		return err
	})
}

func (r *MFARepo) UseStep(ctx context.Context, userID int, step int64) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE user_mfa SET last_step = $2 WHERE user_id = $1 AND last_step < $2`, userID, step)
	return tag.RowsAffected() == 1, err
}

func (r *MFARepo) UseBackupCode(ctx context.Context, userID int, hash string) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE user_backup_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, hash)
	return tag.RowsAffected() == 1, err
}

func (r *MFARepo) ReplaceBackupCodes(ctx context.Context, userID int, hashes []string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error { return replaceCodes(ctx, tx, userID, hashes) })
}

func replaceCodes(ctx context.Context, tx pgx.Tx, userID int, hashes []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_backup_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO user_backup_codes (user_id, code_hash) SELECT $1, unnest($2::text[])`, userID, hashes)
	return err
}

func (r *MFARepo) BackupCodesLeft(ctx context.Context, userID int) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM user_backup_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// SanctionRepo implements sanction.Repository.
type SanctionRepo struct{ db *pgxpool.Pool }

func NewSanctionRepo(db *pgxpool.Pool) *SanctionRepo { return &SanctionRepo{db: db} }

const sanctionCols = `s.user_id, u.username, s.reason, s.until, COALESCE(s.actor_id, 0), s.created_at`

func scanSanction(row pgx.Row, extra ...any) (sanction.Sanction, error) {
	var s sanction.Sanction
	err := row.Scan(append([]any{&s.UserID, &s.Username, &s.Reason, &s.Until, &s.ActorID, &s.CreatedAt}, extra...)...)
	s.Kind = sanction.KindOf(s.Until)
	return s, notFound(err)
}

func (r *SanctionRepo) Save(ctx context.Context, s sanction.Sanction) (sanction.Sanction, error) {
	return scanSanction(r.db.QueryRow(ctx, `WITH s AS (
			INSERT INTO user_sanctions (user_id, reason, until, actor_id) VALUES ($1, $2, $3, NULLIF($4, 0))
			ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason, until = EXCLUDED.until,
				actor_id = EXCLUDED.actor_id, created_at = now()
			RETURNING *
		) SELECT `+sanctionCols+` FROM s JOIN users u ON u.id = s.user_id`,
		s.UserID, s.Reason, s.Until, s.ActorID))
}

func (r *SanctionRepo) Get(ctx context.Context, userID int) (sanction.Sanction, error) {
	return scanSanction(r.db.QueryRow(ctx, `SELECT `+sanctionCols+` FROM user_sanctions s
		JOIN users u ON u.id = s.user_id WHERE s.user_id = $1`, userID))
}

func (r *SanctionRepo) Delete(ctx context.Context, userID int) error {
	return deleteByID(ctx, r.db, `DELETE FROM user_sanctions WHERE user_id = $1`, userID)
}

func (r *SanctionRepo) Expired(ctx context.Context, now time.Time) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM user_sanctions WHERE until <= $1`, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

func (r *SanctionRepo) List(ctx context.Context, p domain.Paging) ([]sanction.Sanction, int, error) {
	rows, err := r.db.Query(ctx, `SELECT `+sanctionCols+`, count(*) OVER () FROM user_sanctions s
		JOIN users u ON u.id = s.user_id ORDER BY s.created_at DESC, s.user_id DESC LIMIT $1 OFFSET $2`, p.Limit, p.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, total := []sanction.Sanction{}, 0
	for rows.Next() {
		s, err := scanSanction(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// RelationRepo implements relation.Repository.
type RelationRepo struct{ db *pgxpool.Pool }

func NewRelationRepo(db *pgxpool.Pool) *RelationRepo { return &RelationRepo{db: db} }

func (r *RelationRepo) Add(ctx context.Context, userID, targetID int, k relation.Kind) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO user_relations (user_id, target_id, kind) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, userID, targetID, k)
		if pgCode(err) == pgForeignKeyViolation {
			return domain.ErrNotFound
		}
		if err != nil || k != relation.Block {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM follows WHERE (follower_id = $1 AND followee_id = $2)
			OR (follower_id = $2 AND followee_id = $1)`, userID, targetID)
		return err
	})
}

func (r *RelationRepo) Remove(ctx context.Context, userID, targetID int, k relation.Kind) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_relations WHERE user_id = $1 AND target_id = $2 AND kind = $3`, userID, targetID, k)
	return err
}

func (r *RelationRepo) List(ctx context.Context, userID int, k relation.Kind, p domain.Paging) (user.SummaryPage, error) {
	page := user.SummaryPage{Items: []user.Summary{}, Page: p.Page, Limit: p.Limit}
	rows, err := r.db.Query(ctx, `SELECT u.id, u.username, u.display_name, u.avatar_url, count(*) OVER ()
		FROM user_relations x JOIN users u ON u.id = x.target_id
		WHERE x.user_id = $1 AND x.kind = $2
		ORDER BY x.created_at DESC, u.id DESC LIMIT $3 OFFSET $4`, userID, k, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var s user.Summary
		if err := rows.Scan(&s.ID, &s.Username, &s.DisplayName, &s.AvatarURL, &page.Total); err != nil {
			return page, err
		}
		page.Items = append(page.Items, s)
	}
	return page, rows.Err()
}

func (r *RelationRepo) Blocked(ctx context.Context, a, b int) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_relations WHERE kind = 'block'
		AND ((user_id = $1 AND target_id = $2) OR (user_id = $2 AND target_id = $1)))`, a, b).Scan(&ok)
	return ok, err
}

func (r *RelationRepo) Silenced(ctx context.Context, actorID int, recipients []int) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT user_id FROM user_relations
		WHERE target_id = $1 AND user_id = ANY($2)`, actorID, recipients)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}
