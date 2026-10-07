package postgres

import (
	"context"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/category"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoryRepo struct{ db *pgxpool.Pool }

func NewCategoryRepo(db *pgxpool.Pool) *CategoryRepo { return &CategoryRepo{db: db} }

func (r *CategoryRepo) List(ctx context.Context) ([]category.Category, error) {
	rows, err := r.db.Query(ctx, `SELECT c.id, c.name, count(p.id)
		FROM categories c LEFT JOIN posts p ON p.category_id = c.id
		GROUP BY c.id ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[category.Category])
}

func (r *CategoryRepo) Exists(ctx context.Context, id int) (bool, error) {
	return exists(ctx, r.db, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1)`, id)
}

func (r *CategoryRepo) Create(ctx context.Context, name string) (category.Category, error) {
	c := category.Category{Name: name}
	err := r.db.QueryRow(ctx, `INSERT INTO categories (name) VALUES ($1) RETURNING id`, name).Scan(&c.ID)
	if pgCode(err) == pgUniqueViolation {
		return c, domain.ErrConflict
	}
	return c, err
}

// Delete leaves posts uncategorized (ON DELETE SET NULL).
func (r *CategoryRepo) Delete(ctx context.Context, id int) error {
	return deleteByID(ctx, r.db, `DELETE FROM categories WHERE id = $1`, id)
}

type StatsRepo struct{ db *pgxpool.Pool }

func NewStatsRepo(db *pgxpool.Pool) *StatsRepo { return &StatsRepo{db: db} }

func (r *StatsRepo) Stats(ctx context.Context) (application.Stats, error) {
	var s application.Stats
	err := r.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM posts),
		(SELECT count(*) FROM comments),
		(SELECT COALESCE(sum(likes), 0) FROM posts),
		(SELECT count(*) FROM categories),
		(SELECT count(*) FROM users)`).Scan(&s.Posts, &s.Comments, &s.Likes, &s.Categories, &s.Users)
	return s, err
}
