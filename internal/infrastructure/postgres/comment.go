package postgres

import (
	"context"
	"errors"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/comment"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CommentRepo struct{ db *pgxpool.Pool }

func NewCommentRepo(db *pgxpool.Pool) *CommentRepo { return &CommentRepo{db: db} }

// commentCols resolves the author from users so renames show up; the
// stored author is the fallback for deleted accounts.
const commentCols = `id, post_id, COALESCE(parent_id, 0), COALESCE(user_id, 0),
	COALESCE((SELECT username FROM users WHERE id = comments.user_id), author), text, created_at, edited_at, likes`

// page lists comments matching where ($1 = arg) in order. With a cursor it
// continues after the cursor's id: ascending when asc, else descending.
func (r *CommentRepo) page(ctx context.Context, where string, arg any, viewerID int, p domain.Paging, asc bool) (comment.Page, error) {
	page := comment.Page{Items: []comment.Comment{}, Page: p.Page, Limit: p.Limit}
	after, offset := int64(0), p.Offset()
	if p.Cursor != "" {
		c, err := domain.DecodeCursor(p.Cursor, 1)
		if err != nil {
			return page, err
		}
		after, offset, page.Page = c[0], 0, 0
	}
	order, cmp := "id DESC", "<"
	if asc {
		order, cmp = "id", ">"
	}
	rows, err := r.db.Query(ctx, `SELECT `+commentCols+`,
			EXISTS (SELECT 1 FROM comment_likes WHERE comment_id = comments.id AND user_id = $5),
			count(*) OVER ()
		FROM comments WHERE `+where+` AND ($4 = 0 OR id `+cmp+` $4)
		ORDER BY `+order+` LIMIT $2 OFFSET $3`, arg, p.Limit, offset, after, viewerID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var c comment.Comment
		if err := rows.Scan(&c.ID, &c.PostID, &c.ParentID, &c.UserID, &c.Author, &c.Text, &c.CreatedAt, &c.EditedAt,
			&c.Likes, &c.Liked, &page.Total); err != nil {
			return page, err
		}
		page.Items = append(page.Items, c)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if n := len(page.Items); n == p.Limit && offset+n < page.Total {
		page.NextCursor = domain.EncodeCursor(int64(page.Items[n-1].ID))
	}
	return page, nil
}

func (r *CommentRepo) ListByPost(ctx context.Context, postID, viewerID int, p domain.Paging) (comment.Page, error) {
	ok, err := exists(ctx, r.db, `SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1)`, postID)
	if err != nil {
		return comment.Page{}, err
	}
	if !ok {
		return comment.Page{}, domain.ErrNotFound
	}
	return r.page(ctx, `post_id = $1`, postID, viewerID, p, true)
}

func (r *CommentRepo) ListAll(ctx context.Context, p domain.Paging) (comment.Page, error) {
	return r.page(ctx, `$1::int IS NOT NULL`, 0, 0, p, false)
}

func (r *CommentRepo) Get(ctx context.Context, id int) (comment.Comment, error) {
	rows, err := r.db.Query(ctx, `SELECT `+commentCols+` FROM comments WHERE id = $1`, id)
	if err != nil {
		return comment.Comment{}, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[comment.Comment])
	return c, notFound(err)
}

// Add checks that a parent belongs to the same post in the INSERT itself,
// so a parent deleted concurrently cannot slip in between check and write.
// No row inserted means ErrBadParent; a missing post is ErrNotFound (FK).
func (r *CommentRepo) Add(ctx context.Context, c comment.Comment) (comment.Comment, error) {
	err := r.db.QueryRow(ctx, `INSERT INTO comments (post_id, parent_id, user_id, author, text)
		SELECT $1, NULLIF($2, 0), NULLIF($3, 0), $4, $5
		WHERE $2 = 0 OR EXISTS (SELECT 1 FROM comments WHERE id = $2 AND post_id = $1)
		RETURNING id, created_at`,
		c.PostID, c.ParentID, c.UserID, c.Author, c.Text).Scan(&c.ID, &c.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return c, comment.ErrBadParent
	case pgCode(err) == pgForeignKeyViolation:
		return c, domain.ErrNotFound
	}
	return c, err
}

func (r *CommentRepo) Edit(ctx context.Context, id int, text string) (comment.Comment, error) {
	rows, err := r.db.Query(ctx, `UPDATE comments SET text = $2, edited_at = now() WHERE id = $1 RETURNING `+commentCols, id, text)
	if err != nil {
		return comment.Comment{}, err
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[comment.Comment])
	return c, notFound(err)
}

// Delete also removes replies (ON DELETE CASCADE).
func (r *CommentRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM comments WHERE id = $1`, id)
}

func (r *CommentRepo) Like(ctx context.Context, id, userID int) (comment.Comment, bool, error) {
	var created bool
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO comment_likes (user_id, comment_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, id)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		created = true
		_, err = tx.Exec(ctx, `UPDATE comments SET likes = likes + 1 WHERE id = $1`, id)
		return err
	})
	if pgCode(err) == pgForeignKeyViolation {
		return comment.Comment{}, false, domain.ErrNotFound
	}
	if err != nil {
		return comment.Comment{}, false, err
	}
	c, err := r.Get(ctx, id)
	c.Liked = err == nil
	return c, created, err
}

func (r *CommentRepo) Unlike(ctx context.Context, id, userID int) (comment.Comment, error) {
	_, err := r.db.Exec(ctx, `WITH del AS (
			DELETE FROM comment_likes WHERE user_id = $2 AND comment_id = $1 RETURNING 1
		)
		UPDATE comments SET likes = likes - (SELECT count(*) FROM del) WHERE id = $1`, id, userID)
	if err != nil {
		return comment.Comment{}, err
	}
	return r.Get(ctx, id)
}
