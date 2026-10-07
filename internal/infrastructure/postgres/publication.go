package postgres

import (
	"context"
	"strings"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/publication"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PublicationRepo struct{ db *pgxpool.Pool }

func NewPublicationRepo(db *pgxpool.Pool) *PublicationRepo { return &PublicationRepo{db: db} }

// publicationCols needs $viewer bound to the viewer's user id.
const publicationCols = `pb.id, pb.slug, pb.name, pb.description, pb.avatar_url, pb.created_at,
	(SELECT count(*) FROM publication_members WHERE publication_id = pb.id),
	(SELECT count(*) FROM posts WHERE publication_id = pb.id AND status = 'published'),
	COALESCE((SELECT role FROM publication_members WHERE publication_id = pb.id AND user_id = $viewer), '')`

func scanPublication(row pgx.Row) (publication.Publication, error) {
	var p publication.Publication
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.AvatarURL, &p.CreatedAt, &p.Members, &p.Posts, &p.MyRole)
	return p, notFound(err)
}

func cols(viewerArg string) string {
	return strings.ReplaceAll(publicationCols, "$viewer", viewerArg)
}

func (r *PublicationRepo) Create(ctx context.Context, in publication.PublicationInput, ownerID int) (publication.Publication, error) {
	var id int
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO publications (slug, name, description, avatar_url)
			VALUES ($1, $2, $3, $4) RETURNING id`, in.Slug, in.Name, in.Description, in.AvatarURL).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO publication_members (publication_id, user_id, role) VALUES ($1, $2, 'owner')`, id, ownerID)
		return err
	})
	if pgCode(err) == pgUniqueViolation {
		return publication.Publication{}, domain.ErrConflict
	}
	if err != nil {
		return publication.Publication{}, err
	}
	return r.GetBySlug(ctx, in.Slug, ownerID)
}

func (r *PublicationRepo) GetBySlug(ctx context.Context, slug string, viewerID int) (publication.Publication, error) {
	return scanPublication(r.db.QueryRow(ctx, `SELECT `+cols("$2")+` FROM publications pb WHERE pb.slug = lower($1)`, slug, viewerID))
}

func (r *PublicationRepo) Update(ctx context.Context, id int, in publication.PublicationInput) error {
	return execOne(ctx, r.db, `UPDATE publications SET name = $2, description = $3, avatar_url = $4 WHERE id = $1`,
		id, in.Name, in.Description, in.AvatarURL)
}

// Delete leaves the publication's posts on their authors (ON DELETE SET NULL).
func (r *PublicationRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM publications WHERE id = $1`, id)
}

func (r *PublicationRepo) ListForUser(ctx context.Context, userID int) ([]publication.Publication, error) {
	rows, err := r.db.Query(ctx, `SELECT `+cols("$1")+` FROM publications pb
		JOIN publication_members m ON m.publication_id = pb.id AND m.user_id = $1 ORDER BY pb.name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (publication.Publication, error) { return scanPublication(row) })
}

func (r *PublicationRepo) Role(ctx context.Context, publicationID, userID int) (publication.Role, error) {
	var role publication.Role
	err := r.db.QueryRow(ctx, `SELECT role FROM publication_members WHERE publication_id = $1 AND user_id = $2`,
		publicationID, userID).Scan(&role)
	return role, ignoreNoRows(err)
}

func (r *PublicationRepo) Members(ctx context.Context, publicationID int) ([]publication.Member, error) {
	rows, err := r.db.Query(ctx, `SELECT u.id, u.username, u.display_name, u.avatar_url, m.role, m.created_at
		FROM publication_members m JOIN users u ON u.id = m.user_id
		WHERE m.publication_id = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, u.username`, publicationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[publication.Member])
}

func (r *PublicationRepo) SetMember(ctx context.Context, publicationID, userID int, role publication.Role) error {
	_, err := r.db.Exec(ctx, `INSERT INTO publication_members (publication_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (publication_id, user_id) DO UPDATE SET role = EXCLUDED.role`, publicationID, userID, role)
	if pgCode(err) == pgForeignKeyViolation {
		return domain.ErrNotFound
	}
	return err
}

func (r *PublicationRepo) RemoveMember(ctx context.Context, publicationID, userID int) error {
	return execOne(ctx, r.db, `DELETE FROM publication_members WHERE publication_id = $1 AND user_id = $2`,
		publicationID, userID)
}
