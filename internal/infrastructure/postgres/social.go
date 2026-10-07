package postgres

import (
	"context"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SocialRepo struct{ db *pgxpool.Pool }

func NewSocialRepo(db *pgxpool.Pool) *SocialRepo { return &SocialRepo{db: db} }

func (r *SocialRepo) Bookmark(ctx context.Context, userID, postID int) error {
	_, err := r.db.Exec(ctx, `INSERT INTO bookmarks (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, postID)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *SocialRepo) Unbookmark(ctx context.Context, userID, postID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM bookmarks WHERE user_id = $1 AND post_id = $2`, userID, postID)
	return err
}

func (r *SocialRepo) FollowTag(ctx context.Context, userID int, tag string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO tag_follows (user_id, tag) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, tag)
	return err
}

func (r *SocialRepo) UnfollowTag(ctx context.Context, userID int, tag string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM tag_follows WHERE user_id = $1 AND tag = $2`, userID, tag)
	return err
}

func (r *SocialRepo) FollowedTags(ctx context.Context, userID int) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT tag FROM tag_follows WHERE user_id = $1 ORDER BY tag`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

const notificationCols = `n.id, n.user_id, n.type, COALESCE(n.actor_id, 0), COALESCE(a.username, ''),
	COALESCE(n.post_id, 0), COALESCE(n.comment_id, 0), n.read_at, n.created_at`

func (r *SocialRepo) Notify(ctx context.Context, n social.Notification, recipients []int) ([]social.Notification, error) {
	rows, err := r.db.Query(ctx, `WITH ins AS (
			INSERT INTO notifications (user_id, type, actor_id, post_id, comment_id)
			SELECT unnest($1::int[]), $2, NULLIF($3, 0), NULLIF($4, 0), NULLIF($5, 0)
			-- notifications_new_post_dedupe_idx: a retried new_post fanout
			-- inserts nothing twice; other types never conflict.
			ON CONFLICT (user_id, type, post_id, actor_id) WHERE type = 'new_post' DO NOTHING
			RETURNING *
		)
		SELECT `+notificationCols+` FROM ins n LEFT JOIN users a ON a.id = n.actor_id`,
		recipients, n.Type, n.ActorID, n.PostID, n.CommentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []social.Notification
	for rows.Next() {
		var s social.Notification
		if err := rows.Scan(&s.ID, &s.UserID, &s.Type, &s.ActorID, &s.Actor, &s.PostID, &s.CommentID, &s.ReadAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Notifications reads the counts and the page in one REPEATABLE READ
// snapshot so Total/Unread agree with Items; one extra row decides
// NextCursor without a second round trip.
func (r *SocialRepo) Notifications(ctx context.Context, userID int, p domain.Paging) (social.NotificationPage, error) {
	page := social.NotificationPage{Items: []social.Notification{}, Page: p.Page, Limit: p.Limit}
	after, offset := int64(0), p.Offset()
	if p.Cursor != "" {
		c, err := domain.DecodeCursor(p.Cursor, 1)
		if err != nil {
			return page, err
		}
		after, offset, page.Page = c[0], 0, 0
	}
	err := pgx.BeginTxFunc(ctx, r.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE read_at IS NULL)
			FROM notifications WHERE user_id = $1`, userID).Scan(&page.Total, &page.Unread); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+notificationCols+` FROM notifications n LEFT JOIN users a ON a.id = n.actor_id
			WHERE n.user_id = $1 AND ($4 = 0 OR n.id < $4) ORDER BY n.id DESC LIMIT $2 OFFSET $3`, userID, p.Limit+1, offset, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s social.Notification
			if err := rows.Scan(&s.ID, &s.UserID, &s.Type, &s.ActorID, &s.Actor, &s.PostID, &s.CommentID, &s.ReadAt, &s.CreatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, s)
		}
		return rows.Err()
	})
	if err != nil {
		return page, err
	}
	if p.Limit > 0 && len(page.Items) > p.Limit {
		page.Items = page.Items[:p.Limit]
		page.NextCursor = domain.EncodeCursor(page.Items[p.Limit-1].ID)
	}
	return page, nil
}

// MarkRead marks the given ids as read; no ids marks nothing.
func (r *SocialRepo) MarkRead(ctx context.Context, userID int, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `UPDATE notifications SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL AND id = ANY($2::bigint[])`, userID, ids)
	return err
}

// MarkAllRead marks every unread notification of the user as read.
func (r *SocialRepo) MarkAllRead(ctx context.Context, userID int) error {
	_, err := r.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	return err
}

func (r *SocialRepo) Hide(ctx context.Context, userID int, k social.HideKind, target string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO feed_hides (user_id, kind, target) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, k, target)
	return err
}

func (r *SocialRepo) Unhide(ctx context.Context, userID int, k social.HideKind, target string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM feed_hides WHERE user_id = $1 AND kind = $2 AND target = $3`, userID, k, target)
	return err
}

func (r *SocialRepo) Hidden(ctx context.Context, userID int, p domain.Paging) (social.HidePage, error) {
	page := social.HidePage{Items: []social.Hide{}, Page: p.Page, Limit: p.Limit}
	rows, err := r.db.Query(ctx, `SELECT h.kind, CASE WHEN h.kind = 'author' THEN COALESCE(u.username, h.target) ELSE h.target END,
			h.created_at, count(*) OVER ()
		FROM feed_hides h LEFT JOIN users u ON h.kind = 'author' AND u.id::text = h.target
		WHERE h.user_id = $1 ORDER BY h.created_at DESC, h.kind, h.target LIMIT $2 OFFSET $3`, userID, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var h social.Hide
		if err := rows.Scan(&h.Kind, &h.Target, &h.CreatedAt, &page.Total); err != nil {
			return page, err
		}
		page.Items = append(page.Items, h)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.Total == 0 && p.Offset() > 0 { // past the end: count anyway
		page.Total, err = r.CountHidden(ctx, userID)
		return page, err
	}
	return page, nil
}

func (r *SocialRepo) CountHidden(ctx context.Context, userID int) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM feed_hides WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}
