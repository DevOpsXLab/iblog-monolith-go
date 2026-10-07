// Package publication holds team blogs and their members.
package publication

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleWriter Role = "writer"
)

// Rank orders roles; 0 means not a member.
func (r Role) Rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	case RoleWriter:
		return 1
	}
	return 0
}

// CanWrite reports whether a member may post into the publication.
func (r Role) CanWrite() bool { return r.Rank() >= 1 }

// CanEdit reports whether a member may edit anyone's posts and the
// publication's details.
func (r Role) CanEdit() bool { return r.Rank() >= 2 }

// CanManage reports whether r may add, change or remove a member whose role
// is (or becomes) target: owners manage editors and writers, editors manage
// writers. Nobody manages owners.
func (r Role) CanManage(target Role) bool {
	return target != RoleOwner && r.Rank() > target.Rank() && r.CanEdit()
}

type Publication struct {
	ID          int       `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
	Members     int       `json:"members"`
	Posts       int       `json:"posts"`   // published
	MyRole      Role      `json:"my_role"` // of the current viewer, "" if none
}

type Member struct {
	UserID      int       `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	Role        Role      `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// PublicationInput is the editable part of a publication. Slug is set on create only.
type PublicationInput struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatar_url"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)

// Normalize validates in; withSlug checks the slug too (create).
func (in PublicationInput) Normalize(withSlug bool) (PublicationInput, error) {
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.AvatarURL = strings.TrimSpace(in.AvatarURL)
	switch {
	case withSlug && !slugRe.MatchString(in.Slug):
		return in, domain.Invalid("slug: 3-40 chars, a-z, 0-9 and -")
	case in.Name == "":
		return in, domain.Invalid("name required")
	case len(in.Name) > 80:
		return in, domain.Invalid("name too long")
	case len(in.Description) > 500:
		return in, domain.Invalid("description too long")
	case in.AvatarURL != "" && !strings.HasPrefix(in.AvatarURL, "/api/uploads/") &&
		!strings.HasPrefix(in.AvatarURL, "https://") && !strings.HasPrefix(in.AvatarURL, "http://"):
		return in, domain.Invalid("bad avatar_url")
	}
	return in, nil
}

// ValidateMemberRole checks a role given through the members API.
func ValidateMemberRole(r Role) error {
	if r != RoleEditor && r != RoleWriter {
		return domain.Invalid("role: editor or writer")
	}
	return nil
}

// Repository returns domain.ErrNotFound for a missing publication and
// domain.ErrConflict for a taken slug.
type Repository interface {
	// Create stores the publication with ownerID as its owner.
	Create(ctx context.Context, in PublicationInput, ownerID int) (Publication, error)
	// GetBySlug fills MyRole for viewerID (0 = anonymous).
	GetBySlug(ctx context.Context, slug string, viewerID int) (Publication, error)
	Update(ctx context.Context, id int, in PublicationInput) error
	Delete(ctx context.Context, id int) error
	// ListForUser lists publications userID belongs to, MyRole filled.
	ListForUser(ctx context.Context, userID int) ([]Publication, error)

	// Role is userID's role in the publication, "" if not a member.
	Role(ctx context.Context, publicationID, userID int) (Role, error)
	Members(ctx context.Context, publicationID int) ([]Member, error)
	SetMember(ctx context.Context, publicationID, userID int, r Role) error
	RemoveMember(ctx context.Context, publicationID, userID int) error
}
