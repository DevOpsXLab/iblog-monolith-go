package category

import (
	"context"
	"strings"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type Category struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// NormalizeName validates a category name.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", domain.Invalid("name required")
	case len(name) > 50:
		return "", domain.Invalid("name too long")
	}
	return name, nil
}

// Repository returns domain.ErrConflict on duplicate name (case-insensitive)
// and domain.ErrNotFound for a missing category.
type Repository interface {
	List(ctx context.Context) ([]Category, error)
	Exists(ctx context.Context, id int) (bool, error)
	Create(ctx context.Context, name string) (Category, error)
	// Delete leaves the category's posts uncategorized.
	Delete(ctx context.Context, id int) error
}
