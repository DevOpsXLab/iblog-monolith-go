package postgres

import (
	"context"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/readinglist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListRepo implements readinglist.Repository.
type ListRepo struct{ db *pgxpool.Pool }

func NewListRepo(db *pgxpool.Pool) *ListRepo { return &ListRepo{db: db} }

const listCols = `l.id, l.slug, l.name, l.description, l.private, l.user_id, u.username,
	(SELECT count(*) FROM list_items li JOIN posts p ON p.id = li.post_id WHERE li.list_id = l.id AND p.status = 'published'),
	l.created_at, l.updated_at`

func scanList(row pgx.Row, extra ...any) (readinglist.List, error) {
	var l readinglist.List
	err := row.Scan(append([]any{&l.ID, &l.Slug, &l.Name, &l.Description, &l.Private, &l.UserID, &l.Owner, &l.Posts,
		&l.CreatedAt, &l.UpdatedAt}, extra...)...)
	return l, notFound(err)
}

func (r *ListRepo) Create(ctx context.Context, userID int, slug string, in readinglist.ListInput) (readinglist.List, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO lists (user_id, slug, name, description, private) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, userID, slug, in.Name, in.Description, in.Private).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return readinglist.List{}, domain.ErrConflict
	}
	if err != nil {
		return readinglist.List{}, err
	}
	return r.get(ctx, `l.id = $1`, id)
}

func (r *ListRepo) Update(ctx context.Context, id int, in readinglist.ListInput) (readinglist.List, error) {
	if err := execOne(ctx, r.db, `UPDATE lists SET name = $2, description = $3, private = $4, updated_at = now() WHERE id = $1`,
		id, in.Name, in.Description, in.Private); err != nil {
		return readinglist.List{}, err
	}
	return r.get(ctx, `l.id = $1`, id)
}

func (r *ListRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM lists WHERE id = $1`, id)
}

func (r *ListRepo) GetBySlug(ctx context.Context, slug string) (readinglist.List, error) {
	return r.get(ctx, `l.slug = $1`, slug)
}

func (r *ListRepo) get(ctx context.Context, where string, key any) (readinglist.List, error) {
	return scanList(r.db.QueryRow(ctx, `SELECT `+listCols+` FROM lists l JOIN users u ON u.id = l.user_id WHERE `+where, key))
}

func (r *ListRepo) ByUser(ctx context.Context, userID int, private bool, postID int, p domain.Paging) (readinglist.Page, error) {
	page := readinglist.Page{Items: []readinglist.List{}, Page: p.Page, Limit: p.Limit}
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM lists WHERE user_id = $1 AND ($2 OR NOT private)`,
		userID, private).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+listCols+`,
			EXISTS (SELECT 1 FROM list_items WHERE list_id = l.id AND post_id = $3)
		FROM lists l JOIN users u ON u.id = l.user_id
		WHERE l.user_id = $1 AND ($2 OR NOT l.private)
		ORDER BY l.created_at DESC, l.id DESC LIMIT $4 OFFSET $5`, userID, private, postID, p.Limit, p.Offset())
	if err != nil {
		return page, err
	}
	page.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (readinglist.List, error) {
		var has bool
		l, err := scanList(row, &has)
		if postID > 0 {
			l.Contains = &has
		}
		return l, err
	})
	return page, err
}

func (r *ListRepo) Count(ctx context.Context, userID int) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM lists WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (r *ListRepo) Add(ctx context.Context, id, postID int) error {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO list_items (list_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, postID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE lists SET updated_at = now() WHERE id = $1`, id)
		return err
	})
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *ListRepo) Remove(ctx context.Context, id, postID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM list_items WHERE list_id = $1 AND post_id = $2`, id, postID)
	return err
}

// ForYou ranks published posts for userID's home feed:
//
//	3 if the author is followed
//	+ 2 per followed tag
//	+ 0.5 per tag of posts the user clapped, read or bookmarked (up to 5 each)
//	+ 0.5 * ln(1 + claps)
//
// decayed by exp(-age in days / 14), over posts of the last 60 days. Own, already read and hidden posts and
// blocked, muted, banned or deleted authors are left out.
func (r *DiscoveryRepo) ForYou(ctx context.Context, userID, limit, offset int) ([]int, int, error) {
	rows, err := r.db.Query(ctx, `WITH
		fa AS (SELECT followee_id AS id FROM follows WHERE follower_id = $1),
		ft AS (SELECT tag FROM tag_follows WHERE user_id = $1),
		touched AS (
			SELECT post_id FROM likes WHERE user_id = $1
			UNION ALL SELECT post_id FROM post_reads WHERE user_id = $1
			UNION ALL SELECT post_id FROM bookmarks WHERE user_id = $1),
		aff AS (SELECT t.tag, least(count(*), 5) AS w FROM touched s JOIN posts tp ON tp.id = s.post_id, unnest(tp.tags) AS t(tag)
			GROUP BY t.tag)
		SELECT p.id, count(*) OVER () FROM posts p
		WHERE p.status = 'published' AND p.published_at > now() - interval '60 days'
		  AND p.user_id IS NOT NULL AND p.user_id <> $1
		  AND p.user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NOT NULL)
		  AND p.user_id NOT IN (SELECT user_id FROM user_sanctions)
		  AND p.user_id NOT IN (SELECT target_id FROM user_relations WHERE user_id = $1)
		  AND p.user_id NOT IN (SELECT user_id FROM user_relations WHERE target_id = $1 AND kind = 'block')
		  AND NOT EXISTS (SELECT 1 FROM post_reads WHERE post_id = p.id AND user_id = $1)
		  AND `+notHidden("$1")+`
		ORDER BY (
				3 * (p.user_id IN (SELECT id FROM fa))::int
				+ 2 * (SELECT count(*) FROM ft WHERE ft.tag = ANY(p.tags))
				+ 0.5 * COALESCE((SELECT sum(w) FROM aff WHERE aff.tag = ANY(p.tags)), 0)
				+ 0.5 * ln(1 + p.claps)
			) * exp(-extract(epoch FROM now() - COALESCE(p.published_at, p.created_at)) / 86400.0 / 14) DESC,
			p.published_at DESC NULLS LAST, p.id DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []int
	total := 0
	for rows.Next() {
		var id int
		if err := rows.Scan(&id, &total); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	return ids, total, rows.Err()
}
