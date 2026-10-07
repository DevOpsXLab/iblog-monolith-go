package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostRepo struct{ db *pgxpool.Pool }

func NewPostRepo(db *pgxpool.Pool) *PostRepo { return &PostRepo{db: db} }

// postCols selects a post with its labels and comment count.
const postCols = `p.id, p.title, p.subtitle, p.slug, p.status, p.body, p.reading_time, p.publish_at, p.published_at,
	COALESCE((SELECT username FROM users WHERE id = p.user_id), p.author), COALESCE(p.category_id, 0), COALESCE(p.publication_id, 0), COALESCE(p.user_id, 0), p.cover_url, p.tags, p.likes, p.claps,
	(SELECT count(*) FROM comments c WHERE c.post_id = p.id),
	COALESCE((SELECT json_agg(json_build_object('id', l.id, 'name', l.name, 'color', l.color) ORDER BY l.name)
		FROM post_labels pl JOIN labels l ON l.id = pl.label_id WHERE pl.post_id = p.id), '[]'),
	p.created_at, p.updated_at, p.canonical_url`

// notHidden is a WHERE condition leaving out posts, authors and tags the
// user in parameter param hid ("show less like this").
func notHidden(param string) string {
	return `NOT EXISTS (SELECT 1 FROM feed_hides h WHERE h.user_id = ` + param + `
		AND ((h.kind = 'post' AND h.target = p.id::text)
		  OR (h.kind = 'author' AND h.target = p.user_id::text)
		  OR (h.kind = 'tag' AND h.target = ANY(p.tags))))`
}

// scanPost scans postCols followed by any extra destinations.
func scanPost(row pgx.Row, extra ...any) (post.Post, error) {
	var p post.Post
	dest := append([]any{&p.ID, &p.Title, &p.Subtitle, &p.Slug, &p.Status, &p.Body, &p.ReadingTime, &p.PublishAt,
		&p.PublishedAt, &p.Author, &p.CategoryID, &p.PublicationID, &p.UserID, &p.CoverURL, &p.Tags, &p.Likes, &p.Claps, &p.CommentsCount,
		&p.Labels, &p.CreatedAt, &p.UpdatedAt, &p.CanonicalURL}, extra...)
	return p, notFound(row.Scan(dest...))
}

func (r *PostRepo) List(ctx context.Context, f post.Filter) (post.Page, error) {
	page := post.Page{Items: []post.Post{}, Page: f.Page, Limit: f.Limit}
	if f.Status == "" {
		f.Status = post.StatusPublished
	}
	var after *time.Time
	afterID, offset := 0, f.Offset()
	if f.Cursor != "" {
		if f.Query != "" {
			return page, domain.Invalid("cursor does not work with q; use page")
		}
		c, err := post.ParseCursor(f.Cursor)
		if err != nil {
			return page, err
		}
		after, afterID, offset, page.Page = &c.At, c.ID, 0, 0
	}
	// Only set filters become predicates, so the planner sees a plain
	// conjunction it can match to indexes (no "$n = 0 OR ..." branches).
	// Every value is still a bound parameter.
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where := []string{"p.status = " + arg(f.Status),
		"(p.user_id IS NULL OR p.user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NOT NULL))"}
	order := "COALESCE(p.published_at, p.publish_at, p.updated_at) DESC, p.id DESC"
	if f.Query != "" {
		q := arg(f.Query)
		// posts_title_trgm_idx (pg_trgm) serves the ILIKE; posts_search_idx the tsquery.
		where = append(where, "(p.search @@ websearch_to_tsquery('simple', "+q+") OR p.title ILIKE '%' || "+q+" || '%')")
		order = "ts_rank(p.search, websearch_to_tsquery('simple', " + q + ")) DESC, " + order
	}
	if f.Tag != "" {
		where = append(where, arg(f.Tag)+" = ANY(p.tags)")
	}
	if f.CategoryID != 0 {
		where = append(where, "p.category_id = "+arg(f.CategoryID))
	}
	if f.UserID != 0 {
		where = append(where, "p.user_id = "+arg(f.UserID))
	}
	if f.FeedOf != 0 {
		u := arg(f.FeedOf)
		where = append(where, "(p.user_id IN (SELECT followee_id FROM follows WHERE follower_id = "+u+
			") OR p.tags && ARRAY(SELECT tag FROM tag_follows WHERE user_id = "+u+"))")
	}
	if f.LabelID != 0 {
		where = append(where, "EXISTS (SELECT 1 FROM post_labels WHERE post_id = p.id AND label_id = "+arg(f.LabelID)+")")
	}
	if f.BookmarkedBy != 0 {
		where = append(where, "EXISTS (SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = "+arg(f.BookmarkedBy)+")")
	}
	if f.PublicationID != 0 {
		where = append(where, "p.publication_id = "+arg(f.PublicationID))
	}
	if after != nil {
		where = append(where, "(COALESCE(p.published_at, p.publish_at, p.updated_at), p.id) < ("+
			arg(*after)+"::timestamptz, "+arg(afterID)+")")
	}
	if f.InList != 0 {
		where = append(where, "EXISTS (SELECT 1 FROM list_items WHERE post_id = p.id AND list_id = "+arg(f.InList)+")")
	}
	if f.HideFor != 0 {
		where = append(where, notHidden(arg(f.HideFor)))
	}
	sql := `SELECT ` + postCols + `, count(*) OVER () FROM posts p
		WHERE ` + strings.Join(where, "\n\t\t  AND ") + `
		ORDER BY ` + order + `
		LIMIT ` + arg(f.Limit) + ` OFFSET ` + arg(offset)
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPost(rows, &page.Total)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, p)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if f.Query == "" && len(page.Items) == f.Limit && offset+f.Limit < page.Total {
		page.NextCursor = post.CursorAfter(page.Items[len(page.Items)-1])
	}
	return page, nil
}

func (r *PostRepo) ListByIDs(ctx context.Context, ids []int) ([]post.Post, error) {
	rows, err := r.db.Query(ctx, `SELECT `+postCols+` FROM posts p
		JOIN unnest($1::int[]) WITH ORDINALITY AS o(id, n) ON o.id = p.id
		WHERE p.status = 'published' ORDER BY o.n`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (post.Post, error) { return scanPost(row) })
}

func (r *PostRepo) Get(ctx context.Context, id, viewerID int) (post.Post, error) {
	return r.getWhere(ctx, `p.id = $1`, id, viewerID)
}

func (r *PostRepo) GetBySlug(ctx context.Context, slug string, viewerID int) (post.Post, error) {
	return r.getWhere(ctx, `p.slug = $1`, slug, viewerID)
}

func (r *PostRepo) getWhere(ctx context.Context, where string, key any, viewerID int) (post.Post, error) {
	var myClaps int
	var bookmarked bool
	p, err := scanPost(r.db.QueryRow(ctx, `SELECT `+postCols+`,
			COALESCE((SELECT claps FROM likes WHERE post_id = p.id AND user_id = $2), 0),
			EXISTS (SELECT 1 FROM bookmarks WHERE post_id = p.id AND user_id = $2)
		FROM posts p WHERE `+where, key, viewerID), &myClaps, &bookmarked)
	p.MyClaps, p.Liked, p.Bookmarked = myClaps, myClaps > 0, bookmarked
	return p, err
}

func refErr(err error) error {
	if pgCode(err) == pgForeignKeyViolation {
		switch constraint(err) {
		case "post_labels_label_id_fkey":
			return post.ErrUnknownLabel
		case "posts_publication_id_fkey":
			return domain.ErrNotFound
		}
		return post.ErrUnknownCategory
	}
	return err
}

func (r *PostRepo) Create(ctx context.Context, d post.Draft, authorID int, author string) (post.Post, error) {
	var id int
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO posts (title, subtitle, slug, status, publish_at, body, reading_time,
				published_at, author, category_id, user_id, cover_url, tags, publication_id, canonical_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, CASE WHEN $4 = 'published' THEN now() END,
				$8, NULLIF($9, 0), NULLIF($10, 0), $11, $12, NULLIF($13, 0), $14) RETURNING id`,
			d.Title, d.Subtitle, post.NewSlug(d.Title), d.Status, d.PublishAt, d.Body, post.ReadingTime(d.Body),
			author, d.CategoryID, authorID, d.CoverURL, d.Tags, d.PublicationID, d.CanonicalURL).Scan(&id)
		if err != nil {
			return err
		}
		return setLabels(ctx, tx, id, d.LabelIDs)
	})
	if err != nil {
		return post.Post{}, refErr(err)
	}
	return r.Get(ctx, id, 0)
}

func (r *PostRepo) Update(ctx context.Context, id int, d post.Draft) (post.Post, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error { return updatePost(ctx, tx, id, d) })
	if err != nil {
		return post.Post{}, refErr(err)
	}
	return r.Get(ctx, id, 0)
}

// UpdateWithRevision updates the post and, when title, subtitle or body
// change, stores the previous text as the next revision, all in one
// transaction. The post row is locked first (FOR UPDATE), so the snapshot
// is the row actually being replaced and versions cannot race; a failed
// revision insert rolls the edit back.
func (r *PostRepo) UpdateWithRevision(ctx context.Context, id, editorID int, d post.Draft) (post.Post, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var title, subtitle, body string
		if err := tx.QueryRow(ctx, `SELECT title, subtitle, body FROM posts WHERE id = $1 FOR UPDATE`, id).
			Scan(&title, &subtitle, &body); err != nil {
			return notFound(err)
		}
		if title != d.Title || subtitle != d.Subtitle || body != d.Body {
			if _, err := tx.Exec(ctx, `INSERT INTO post_revisions (post_id, version, title, subtitle, body, editor_id)
				SELECT $1, COALESCE(max(version), 0) + 1, $2, $3, $4, NULLIF($5, 0)
				FROM post_revisions WHERE post_id = $1`, id, title, subtitle, body, editorID); err != nil {
				return err
			}
		}
		return updatePost(ctx, tx, id, d)
	})
	if err != nil {
		return post.Post{}, refErr(err)
	}
	return r.Get(ctx, id, 0)
}

func updatePost(ctx context.Context, tx pgx.Tx, id int, d post.Draft) error {
	tag, err := tx.Exec(ctx, `UPDATE posts
		SET title = $2, body = $3, category_id = NULLIF($4, 0), cover_url = $5, tags = $6,
			subtitle = $7, status = $8, reading_time = $9, publish_at = $10, publication_id = NULLIF($11, 0),
			canonical_url = $12, updated_at = now(),
			published_at = CASE WHEN $8 = 'published' THEN COALESCE(published_at, now()) END
		WHERE id = $1`,
		id, d.Title, d.Body, d.CategoryID, d.CoverURL, d.Tags, d.Subtitle, d.Status, post.ReadingTime(d.Body), d.PublishAt, d.PublicationID,
		d.CanonicalURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return setLabels(ctx, tx, id, d.LabelIDs)
}

func setLabels(ctx context.Context, tx pgx.Tx, postID int, ids []int) error {
	if _, err := tx.Exec(ctx, `DELETE FROM post_labels WHERE post_id = $1`, postID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO post_labels (post_id, label_id) SELECT $1, unnest($2::int[])`, postID, ids)
	return err
}

// Delete also removes the post's comments, likes, labels and bookmarks (ON DELETE CASCADE).
func (r *PostRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM posts WHERE id = $1`, id)
}

func (r *PostRepo) Publish(ctx context.Context, id int, now time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE posts SET status = 'published', published_at = $2, publish_at = NULL
		WHERE id = $1 AND status = 'scheduled' AND publish_at <= $2`, id, now)
	return tag.RowsAffected() > 0, err
}

func (r *PostRepo) DueScheduled(ctx context.Context, now time.Time) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT id FROM posts WHERE status = 'scheduled' AND publish_at <= $1 ORDER BY publish_at`, now)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// Like is a single clap that does nothing if the user already clapped.
func (r *PostRepo) Like(ctx context.Context, id, userID int) (post.Post, bool, error) {
	var created bool
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO likes (user_id, post_id) VALUES ($2, $1) ON CONFLICT DO NOTHING`, id, userID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		created = true
		_, err = tx.Exec(ctx, `UPDATE posts SET likes = likes + 1, claps = claps + 1 WHERE id = $1`, id)
		return err
	})
	return r.afterClap(ctx, id, userID, created, err)
}

// Clap adds n claps in one transaction. A new like row is inserted first
// (ON CONFLICT DO NOTHING tells whether it is the first clap); otherwise the
// row is locked, so concurrent claps by one user cannot pass MaxClaps.
func (r *PostRepo) Clap(ctx context.Context, id, userID, n int) (post.Post, bool, error) {
	var created bool
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		n = min(n, post.MaxClaps)
		tag, err := tx.Exec(ctx, `INSERT INTO likes (user_id, post_id, claps) VALUES ($2, $1, $3)
			ON CONFLICT DO NOTHING`, id, userID, n)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			created = true
			_, err = tx.Exec(ctx, `UPDATE posts SET likes = likes + 1, claps = claps + $2 WHERE id = $1`, id, n)
			return err
		}
		var before int
		err = tx.QueryRow(ctx, `SELECT claps FROM likes WHERE user_id = $2 AND post_id = $1 FOR UPDATE`, id, userID).Scan(&before)
		if err != nil {
			return err
		}
		added := min(before+n, post.MaxClaps) - before
		if added == 0 {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE likes SET claps = claps + $3 WHERE user_id = $2 AND post_id = $1`, id, userID, added); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE posts SET claps = claps + $2 WHERE id = $1`, id, added)
		return err
	})
	return r.afterClap(ctx, id, userID, created, err)
}

func (r *PostRepo) afterClap(ctx context.Context, id, userID int, created bool, err error) (post.Post, bool, error) {
	if pgCode(err) == pgForeignKeyViolation {
		return post.Post{}, false, domain.ErrNotFound
	}
	if err != nil {
		return post.Post{}, false, err
	}
	p, err := r.Get(ctx, id, userID)
	return p, created, err
}

func (r *PostRepo) Unlike(ctx context.Context, id, userID int) (post.Post, error) {
	_, err := r.db.Exec(ctx, `WITH del AS (
			DELETE FROM likes WHERE user_id = $2 AND post_id = $1 RETURNING claps
		)
		UPDATE posts SET likes = likes - (SELECT count(*) FROM del),
			claps = claps - (SELECT COALESCE(sum(claps), 0) FROM del) WHERE id = $1`, id, userID)
	if err != nil {
		return post.Post{}, err
	}
	return r.Get(ctx, id, userID)
}

func (r *PostRepo) Tags(ctx context.Context) ([]post.TagCount, error) {
	rows, err := r.db.Query(ctx, `SELECT tag, count(*) FROM posts, unnest(tags) AS tag
		WHERE status = 'published' GROUP BY tag ORDER BY count(*) DESC, tag`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[post.TagCount])
}

func (r *PostRepo) Labels(ctx context.Context) ([]post.Label, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, color FROM labels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[post.Label])
}

func (r *PostRepo) CreateLabel(ctx context.Context, l post.Label) (post.Label, error) {
	err := r.db.QueryRow(ctx, `INSERT INTO labels (name, color) VALUES ($1, $2) RETURNING id`, l.Name, l.Color).Scan(&l.ID)
	if pgCode(err) == pgUniqueViolation {
		return l, domain.ErrConflict
	}
	return l, err
}

func (r *PostRepo) DeleteLabel(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM labels WHERE id = $1`, id)
}

const highlightCols = `id, post_id, user_id, text, start_offset, end_offset, created_at`

func (r *PostRepo) AddHighlight(ctx context.Context, h post.Highlight) (post.Highlight, error) {
	err := r.db.QueryRow(ctx, `INSERT INTO highlights (user_id, post_id, text, start_offset, end_offset)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		h.UserID, h.PostID, h.Text, h.Start, h.End).Scan(&h.ID, &h.CreatedAt)
	if pgCode(err) == pgForeignKeyViolation {
		return h, domain.ErrNotFound
	}
	return h, err
}

func (r *PostRepo) GetHighlight(ctx context.Context, id int) (post.Highlight, error) {
	rows, err := r.db.Query(ctx, `SELECT `+highlightCols+` FROM highlights WHERE id = $1`, id)
	if err != nil {
		return post.Highlight{}, err
	}
	h, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[post.Highlight])
	return h, notFound(err)
}

func (r *PostRepo) DeleteHighlight(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM highlights WHERE id = $1`, id)
}

func (r *PostRepo) Highlights(ctx context.Context, postID, viewerID, top int) (post.Highlights, error) {
	out := post.Highlights{Top: []post.TopHighlight{}, Mine: []post.Highlight{}}
	rows, err := r.db.Query(ctx, `SELECT text, start_offset, end_offset, count(DISTINCT user_id) FROM highlights
		WHERE post_id = $1 GROUP BY text, start_offset, end_offset
		ORDER BY count(DISTINCT user_id) DESC, start_offset LIMIT $2`, postID, top)
	if err != nil {
		return out, err
	}
	if out.Top, err = pgx.CollectRows(rows, pgx.RowToStructByPos[post.TopHighlight]); err != nil || viewerID == 0 {
		return out, err
	}
	rows, err = r.db.Query(ctx, `SELECT `+highlightCols+` FROM highlights
		WHERE post_id = $1 AND user_id = $2 ORDER BY start_offset`, postID, viewerID)
	if err != nil {
		return out, err
	}
	out.Mine, err = pgx.CollectRows(rows, pgx.RowToStructByPos[post.Highlight])
	return out, err
}

func (r *PostRepo) AuthorStats(ctx context.Context, userID int, pg domain.Paging) ([]post.AuthorStat, int, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id, p.title, p.slug, p.status, p.published_at, p.likes, p.claps,
			(SELECT count(*) FROM comments WHERE post_id = p.id),
			(SELECT count(*) FROM bookmarks WHERE post_id = p.id),
			(SELECT count(*) FROM highlights WHERE post_id = p.id),
			(SELECT count(*) FROM post_reads WHERE post_id = p.id),
			count(*) OVER ()
		FROM posts p WHERE p.user_id = $1
		ORDER BY COALESCE(p.published_at, p.updated_at) DESC, p.id DESC LIMIT $2 OFFSET $3`,
		userID, pg.Limit, pg.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	stats, total := []post.AuthorStat{}, 0
	for rows.Next() {
		var s post.AuthorStat
		if err := rows.Scan(&s.PostID, &s.Title, &s.Slug, &s.Status, &s.PublishedAt, &s.Likes, &s.Claps,
			&s.Comments, &s.Bookmarks, &s.Highlights, &s.Reads, &total); err != nil {
			return nil, 0, err
		}
		stats = append(stats, s)
	}
	return stats, total, rows.Err()
}

func (r *PostRepo) Read(ctx context.Context, postID, userID int, reader string) (bool, error) {
	tag, err := r.db.Exec(ctx, `INSERT INTO post_reads (post_id, reader, user_id) VALUES ($1, $2, NULLIF($3, 0))
		ON CONFLICT DO NOTHING`, postID, reader, userID)
	if pgCode(err) == pgForeignKeyViolation {
		return false, domain.ErrNotFound
	}
	return tag.RowsAffected() > 0, err
}

func (r *PostRepo) DailyReads(ctx context.Context, postID int, since time.Time) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `SELECT to_char(read_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*) FROM post_reads
		WHERE post_id = $1 AND read_at >= $2 GROUP BY 1`, postID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		out[day] = n
	}
	return out, rows.Err()
}
