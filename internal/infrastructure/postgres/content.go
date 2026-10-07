package postgres

import (
	"context"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/revision"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/series"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RevisionRepo implements revision.Repository.
type RevisionRepo struct{ db *pgxpool.Pool }

func NewRevisionRepo(db *pgxpool.Pool) *RevisionRepo { return &RevisionRepo{db: db} }

const revisionCols = `r.id, r.post_id, r.version, r.title, r.subtitle, COALESCE(r.editor_id, 0),
	COALESCE((SELECT username FROM users WHERE id = r.editor_id), ''), r.created_at`

func scanRevision(row pgx.Row, extra ...any) (revision.Revision, error) {
	var v revision.Revision
	err := row.Scan(append([]any{&v.ID, &v.PostID, &v.Version, &v.Title, &v.Subtitle, &v.EditorID, &v.Editor, &v.CreatedAt}, extra...)...)
	return v, notFound(err)
}

func (r *RevisionRepo) Add(ctx context.Context, postID, editorID int, c revision.Content) (revision.Revision, error) {
	var v revision.Revision
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// The post row lock serializes version numbers per post.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM posts WHERE id = $1 FOR UPDATE`, postID); err != nil {
			return err
		}
		var err error
		v, err = scanRevision(tx.QueryRow(ctx, `WITH r AS (
				INSERT INTO post_revisions (post_id, version, title, subtitle, body, editor_id)
				SELECT $1, COALESCE(max(version), 0) + 1, $2, $3, $4, NULLIF($5, 0)
				FROM post_revisions WHERE post_id = $1
				RETURNING *
			) SELECT `+revisionCols+` FROM r`, postID, c.Title, c.Subtitle, c.Body, editorID))
		return err
	})
	if pgCode(err) == pgForeignKeyViolation {
		return v, domain.ErrNotFound
	}
	return v, err
}

func (r *RevisionRepo) List(ctx context.Context, postID int) ([]revision.Revision, error) {
	rows, err := r.db.Query(ctx, `SELECT `+revisionCols+` FROM post_revisions r WHERE r.post_id = $1 ORDER BY r.version DESC`, postID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (revision.Revision, error) { return scanRevision(row) })
}

func (r *RevisionRepo) Get(ctx context.Context, postID, version int) (revision.Revision, error) {
	var body string
	v, err := scanRevision(r.db.QueryRow(ctx, `SELECT `+revisionCols+`, r.body FROM post_revisions r
		WHERE r.post_id = $1 AND r.version = $2`, postID, version), &body)
	v.Body = body
	return v, err
}

// SeriesRepo implements series.Repository.
type SeriesRepo struct{ db *pgxpool.Pool }

func NewSeriesRepo(db *pgxpool.Pool) *SeriesRepo { return &SeriesRepo{db: db} }

const seriesCols = `s.id, s.slug, s.title, s.description, s.user_id, u.username,
	(SELECT count(*) FROM series_posts sp JOIN posts p ON p.id = sp.post_id WHERE sp.series_id = s.id AND p.status = 'published'),
	s.created_at, s.updated_at`

func scanSeries(row pgx.Row) (series.Series, error) {
	var s series.Series
	err := row.Scan(&s.ID, &s.Slug, &s.Title, &s.Description, &s.UserID, &s.Author, &s.Parts, &s.CreatedAt, &s.UpdatedAt)
	return s, notFound(err)
}

func (r *SeriesRepo) Create(ctx context.Context, userID int, slug string, in series.SeriesInput) (series.Series, error) {
	var id int
	err := r.db.QueryRow(ctx, `INSERT INTO series (user_id, slug, title, description) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, slug, in.Title, in.Description).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return series.Series{}, domain.ErrConflict
	}
	if err != nil {
		return series.Series{}, err
	}
	return r.get(ctx, `s.id = $1`, id)
}

func (r *SeriesRepo) Update(ctx context.Context, id int, in series.SeriesInput) (series.Series, error) {
	if err := execOne(ctx, r.db, `UPDATE series SET title = $2, description = $3, updated_at = now() WHERE id = $1`,
		id, in.Title, in.Description); err != nil {
		return series.Series{}, err
	}
	return r.get(ctx, `s.id = $1`, id)
}

func (r *SeriesRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM series WHERE id = $1`, id)
}

func (r *SeriesRepo) GetBySlug(ctx context.Context, slug string) (series.Series, error) {
	return r.get(ctx, `s.slug = $1`, slug)
}

func (r *SeriesRepo) OfPost(ctx context.Context, postID int) (series.Series, error) {
	return r.get(ctx, `s.id = (SELECT series_id FROM series_posts WHERE post_id = $1)`, postID)
}

func (r *SeriesRepo) get(ctx context.Context, where string, key any) (series.Series, error) {
	return scanSeries(r.db.QueryRow(ctx, `SELECT `+seriesCols+` FROM series s JOIN users u ON u.id = s.user_id WHERE `+where, key))
}

func (r *SeriesRepo) ByUser(ctx context.Context, userID int) ([]series.Series, error) {
	rows, err := r.db.Query(ctx, `SELECT `+seriesCols+` FROM series s JOIN users u ON u.id = s.user_id
		WHERE s.user_id = $1 ORDER BY s.created_at DESC, s.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (series.Series, error) { return scanSeries(row) })
}

func (r *SeriesRepo) Parts(ctx context.Context, id int, all bool) ([]series.Part, error) {
	rows, err := r.db.Query(ctx, `SELECT row_number() OVER (ORDER BY sp.position), p.id, p.title, p.slug, p.status, p.published_at
		FROM series_posts sp JOIN posts p ON p.id = sp.post_id
		WHERE sp.series_id = $1 AND ($2 OR p.status = 'published')
		ORDER BY sp.position`, id, all)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (series.Part, error) {
		var p series.Part
		err := row.Scan(&p.Position, &p.PostID, &p.Title, &p.Slug, &p.Status, &p.PublishedAt)
		return p, err
	})
}

// SetPosts replaces the series' parts. The series row lock serializes
// concurrent reorders; duplicate ids are rejected up front.
func (r *SeriesRepo) SetPosts(ctx context.Context, id, ownerID int, postIDs []int) error {
	seen := make(map[int]struct{}, len(postIDs))
	for _, p := range postIDs {
		if _, dup := seen[p]; dup {
			return domain.Invalid("post_ids must be distinct post ids")
		}
		seen[p] = struct{}{}
	}
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var locked int
		if err := tx.QueryRow(ctx, `SELECT id FROM series WHERE id = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return notFound(err)
		}
		var owned int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM posts WHERE id = ANY($1) AND user_id = $2`, postIDs, ownerID).Scan(&owned); err != nil {
			return err
		}
		if owned != len(postIDs) {
			return domain.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM series_posts WHERE series_id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO series_posts (series_id, post_id, position)
			SELECT $1, o.id, o.n FROM unnest($2::int[]) WITH ORDINALITY AS o(id, n)`, id, postIDs)
		if pgCode(err) == pgUniqueViolation {
			switch constraint(err) {
			case "series_posts_post_id_key":
				return series.ErrPostInOtherSeries
			case "series_posts_pkey":
				return domain.Invalid("post_ids must be distinct post ids")
			}
			return err
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE series SET updated_at = now() WHERE id = $1`, id)
		return err
	})
}

// DiscoveryRepo implements application.Discovery.
type DiscoveryRepo struct{ db *pgxpool.Pool }

func NewDiscoveryRepo(db *pgxpool.Pool) *DiscoveryRepo { return &DiscoveryRepo{db: db} }

// RelatedPosts scores published posts by shared tags (2 each), same
// category (1) and same author (1), newest first on ties.
func (r *DiscoveryRepo) RelatedPosts(ctx context.Context, postID, limit int) ([]int, error) {
	rows, err := r.db.Query(ctx, `WITH src AS (SELECT tags, category_id, user_id FROM posts WHERE id = $1)
		SELECT p.id FROM posts p, src
		WHERE p.id <> $1 AND p.status = 'published'
		  AND (p.user_id IS NULL OR p.user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NOT NULL))
		  AND (p.tags && src.tags OR p.category_id = src.category_id)
		ORDER BY 2 * cardinality(ARRAY(SELECT unnest(p.tags) INTERSECT SELECT unnest(src.tags)))
			+ (p.category_id IS NOT DISTINCT FROM src.category_id AND src.category_id IS NOT NULL)::int
			+ (p.user_id = src.user_id)::int DESC,
			p.published_at DESC NULLS LAST, p.id DESC
		LIMIT $2`, postID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// SuggestUsers ranks users followed by people userID follows (3 each),
// writing in tags userID follows (1 per post) and, as a fallback, by
// follower count. Self, followed, blocked and muted users are left out.
func (r *DiscoveryRepo) SuggestUsers(ctx context.Context, userID, limit int) ([]application.SuggestedUser, error) {
	rows, err := r.db.Query(ctx, `WITH
		mine AS (SELECT followee_id AS id FROM follows WHERE follower_id = $1),
		fof AS (SELECT f.followee_id AS id, 3 * count(*) AS score, count(*) AS n FROM follows f
			WHERE f.follower_id IN (SELECT id FROM mine) GROUP BY f.followee_id),
		tags AS (SELECT p.user_id AS id, count(*) AS score FROM posts p
			WHERE p.status = 'published' AND p.tags && ARRAY(SELECT tag FROM tag_follows WHERE user_id = $1)
			GROUP BY p.user_id),
		pop AS (SELECT followee_id AS id, count(*) AS followers FROM follows GROUP BY followee_id),
		cand AS (SELECT u.id,
				COALESCE(fof.score, 0) + COALESCE(tags.score, 0) AS score,
				COALESCE(fof.n, 0) AS mutual, COALESCE(tags.score, 0) AS tag_posts,
				COALESCE(pop.followers, 0) AS followers
			FROM users u
			LEFT JOIN fof ON fof.id = u.id LEFT JOIN tags ON tags.id = u.id LEFT JOIN pop ON pop.id = u.id
			WHERE u.id <> $1 AND u.deleted_at IS NULL
			  AND u.id NOT IN (SELECT id FROM mine)
			  AND u.id NOT IN (SELECT target_id FROM user_relations WHERE user_id = $1)
			  AND u.id NOT IN (SELECT user_id FROM user_relations WHERE target_id = $1 AND kind = 'block')
			  AND u.id NOT IN (SELECT user_id FROM user_sanctions)
			  AND (fof.id IS NOT NULL OR tags.id IS NOT NULL OR pop.id IS NOT NULL
			       OR EXISTS (SELECT 1 FROM posts WHERE user_id = u.id AND status = 'published')))
		SELECT c.id, u.username, u.display_name, u.avatar_url, c.followers, c.mutual, c.tag_posts
		FROM cand c JOIN users u ON u.id = c.id
		ORDER BY c.score DESC, c.followers DESC, c.id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (application.SuggestedUser, error) {
		var s application.SuggestedUser
		var tagPosts int
		err := row.Scan(&s.ID, &s.Username, &s.DisplayName, &s.AvatarURL, &s.Followers, &s.MutualFollows, &tagPosts)
		switch {
		case s.MutualFollows > 0:
			s.Reason = "followed_by_people_you_follow"
		case tagPosts > 0:
			s.Reason = "writes_about_your_tags"
		default:
			s.Reason = "popular"
		}
		return s, err
	})
}

// UserIDs maps usernames (any case) to ids of active users.
func (r *DiscoveryRepo) UserIDs(ctx context.Context, usernames []string) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT id FROM users WHERE lower(username) = ANY($1) AND deleted_at IS NULL`, usernames)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}
