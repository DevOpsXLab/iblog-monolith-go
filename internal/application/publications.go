package application

import (
	"context"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/post"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/publication"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain/user"
)

// requireWriter checks that actor may post into publicationID (0 = personal
// post, always allowed).
func (b *Blog) requireWriter(ctx context.Context, actor user.User, publicationID int) error {
	if publicationID == 0 {
		return nil
	}
	role, err := b.Publications.Role(ctx, publicationID, actor.ID)
	if err != nil {
		return err
	}
	if !role.CanWrite() {
		return domain.ErrForbidden
	}
	return nil
}

func (b *Blog) isPublicationEditor(ctx context.Context, actor user.User, publicationID int) bool {
	if publicationID == 0 || actor.ID == 0 {
		return false
	}
	role, err := b.Publications.Role(ctx, publicationID, actor.ID)
	return err == nil && role.CanEdit()
}

func (b *Blog) CreatePublication(ctx context.Context, actor user.User, in publication.PublicationInput) (publication.Publication, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreatePublication")
	defer span.End()
	if err := b.Authz.Can(ctx, "publication", "create", 0, 0); err != nil {
		return publication.Publication{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return publication.Publication{}, err
	}
	in, err := in.Normalize(true)
	if err != nil {
		return publication.Publication{}, err
	}
	return b.Publications.Create(ctx, in, actor.ID)
}

func (b *Blog) GetPublication(ctx context.Context, slug string, viewerID int) (publication.Publication, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetPublication")
	defer span.End()
	return b.Publications.GetBySlug(ctx, slug, viewerID)
}

// UpdatePublication is allowed for owners and editors.
func (b *Blog) UpdatePublication(ctx context.Context, actor user.User, slug string, in publication.PublicationInput) (publication.Publication, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdatePublication")
	defer span.End()
	pub, err := b.publicationAs(ctx, actor, slug, publication.Role.CanEdit)
	if err != nil {
		return pub, err
	}
	if in, err = in.Normalize(false); err != nil {
		return pub, err
	}
	if err := b.Publications.Update(ctx, pub.ID, in); err != nil {
		return pub, err
	}
	return b.Publications.GetBySlug(ctx, slug, actor.ID)
}

// DeletePublication is allowed for the owner; posts stay with their authors.
func (b *Blog) DeletePublication(ctx context.Context, actor user.User, slug string) error {
	ctx, span := tracer.Start(ctx, "Blog.DeletePublication")
	defer span.End()
	pub, err := b.publicationAs(ctx, actor, slug, func(r publication.Role) bool { return r == publication.RoleOwner })
	if err != nil {
		return err
	}
	if err := b.Publications.Delete(ctx, pub.ID); err != nil {
		return err
	}
	b.Audit.Record(ctx, actor.ID, "publication.delete", "publication:"+itoa(pub.ID), map[string]any{"slug": pub.Slug})
	return nil
}

func (b *Blog) MyPublications(ctx context.Context, actor user.User) ([]publication.Publication, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyPublications")
	defer span.End()
	return b.Publications.ListForUser(ctx, actor.ID)
}

func (b *Blog) PublicationPosts(ctx context.Context, slug string, p domain.Paging) (post.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.PublicationPosts")
	defer span.End()
	pub, err := b.Publications.GetBySlug(ctx, slug, 0)
	if err != nil {
		return post.Page{}, err
	}
	return b.Posts.List(ctx, post.Filter{PublicationID: pub.ID, Status: post.StatusPublished, Paging: p})
}

func (b *Blog) PublicationMembers(ctx context.Context, slug string) ([]publication.Member, error) {
	ctx, span := tracer.Start(ctx, "Blog.PublicationMembers")
	defer span.End()
	pub, err := b.Publications.GetBySlug(ctx, slug, 0)
	if err != nil {
		return nil, err
	}
	return b.Publications.Members(ctx, pub.ID)
}

// SetPublicationMember adds a member or changes their role. Owners manage
// editors and writers; editors manage writers.
func (b *Blog) SetPublicationMember(ctx context.Context, actor user.User, slug, username string, role publication.Role) error {
	ctx, span := tracer.Start(ctx, "Blog.SetPublicationMember")
	defer span.End()
	if err := publication.ValidateMemberRole(role); err != nil {
		return err
	}
	pub, err := b.publicationAs(ctx, actor, slug, func(r publication.Role) bool { return r.CanManage(role) })
	if err != nil {
		return err
	}
	u, err := b.Users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	current, err := b.Publications.Role(ctx, pub.ID, u.ID)
	if err != nil {
		return err
	}
	if current != "" && !pub.MyRole.CanManage(current) {
		return domain.ErrForbidden // e.g. an editor demoting another editor, or anyone touching the owner
	}
	return b.Publications.SetMember(ctx, pub.ID, u.ID, role)
}

// RemovePublicationMember removes a member; members may also leave, except
// the owner.
func (b *Blog) RemovePublicationMember(ctx context.Context, actor user.User, slug, username string) error {
	ctx, span := tracer.Start(ctx, "Blog.RemovePublicationMember")
	defer span.End()
	pub, err := b.Publications.GetBySlug(ctx, slug, actor.ID)
	if err != nil {
		return err
	}
	u, err := b.Users.GetByUsername(ctx, username)
	if err != nil {
		return err
	}
	target, err := b.Publications.Role(ctx, pub.ID, u.ID)
	if err != nil {
		return err
	}
	switch {
	case target == "":
		return domain.ErrNotFound
	case target == publication.RoleOwner:
		return domain.ErrForbidden
	case u.ID != actor.ID && !pub.MyRole.CanManage(target):
		return domain.ErrForbidden
	}
	return b.Publications.RemoveMember(ctx, pub.ID, u.ID)
}

// publicationAs loads a publication and checks the actor's role with ok;
// non-members get ErrForbidden.
func (b *Blog) publicationAs(ctx context.Context, actor user.User, slug string, ok func(publication.Role) bool) (publication.Publication, error) {
	pub, err := b.Publications.GetBySlug(ctx, slug, actor.ID)
	if err != nil {
		return pub, err
	}
	if !ok(pub.MyRole) {
		return pub, domain.ErrForbidden
	}
	return pub, nil
}
