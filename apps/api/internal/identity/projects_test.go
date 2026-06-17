package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
)

func orgOf(t *testing.T, svc *Service, res *AuthResult) string {
	t.Helper()
	orgs, err := svc.ListOrganizations(context.Background(), res.User.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("list orgs: %v", err)
	}
	return orgs[0].ID
}

func TestDefaultProjectCreatedOnSignup(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, _ := svc.Signup(ctx, "p@example.com", "P", "supersecret")
	def, err := svc.DefaultProject(ctx, orgOf(t, svc, res))
	if err != nil {
		t.Fatalf("default project: %v", err)
	}
	if !def.IsDefault || def.Slug != "default" {
		t.Fatalf("unexpected default project: %+v", def)
	}
}

func TestCreateProjectRequiresAdmin(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "o@example.com", "O", "supersecret")
	org := orgOf(t, svc, owner)

	if _, err := svc.CreateProject(ctx, owner.User.ID, org, "Production"); err != nil {
		t.Fatalf("owner create project: %v", err)
	}
	outsider, _ := svc.Signup(ctx, "x@example.com", "X", "supersecret")
	if _, err := svc.CreateProject(ctx, outsider.User.ID, org, "Sneaky"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestInviteToOrgAndAccept(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "owner@example.com", "Owner", "supersecret")
	org := orgOf(t, svc, owner)

	inv, err := svc.Invite(ctx, owner.User.ID, org, "", "invitee@example.com", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if inv.Token == "" || inv.Status != domain.InvitePending {
		t.Fatalf("bad invitation: %+v", inv)
	}

	invitee, _ := svc.Signup(ctx, "invitee@example.com", "Inv", "supersecret")
	if _, err := svc.AcceptInvitation(ctx, invitee.User.ID, "invitee@example.com", inv.Token); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Invitee is now an admin of the org.
	if _, err := svc.Authorize(ctx, invitee.User.ID, org, domain.RoleAdmin); err != nil {
		t.Fatalf("expected invitee to be org admin: %v", err)
	}
	// The invitation cannot be reused.
	if _, err := svc.AcceptInvitation(ctx, invitee.User.ID, "invitee@example.com", inv.Token); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expected reuse to fail, got %v", err)
	}
}

func TestInviteToProjectScopesAccess(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "po@example.com", "PO", "supersecret")
	org := orgOf(t, svc, owner)
	proj, _ := svc.CreateProject(ctx, owner.User.ID, org, "Production")

	inv, err := svc.Invite(ctx, owner.User.ID, org, proj.ID, "dev@example.com", domain.RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	dev, _ := svc.Signup(ctx, "dev@example.com", "Dev", "supersecret")
	if _, err := svc.AcceptInvitation(ctx, dev.User.ID, "dev@example.com", inv.Token); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// dev can act on the invited project...
	if err := svc.AuthorizeProject(ctx, dev.User.ID, org, proj.ID, domain.RoleMember); err != nil {
		t.Fatalf("expected project access: %v", err)
	}
	// ...but is not an org admin...
	if _, err := svc.Authorize(ctx, dev.User.ID, org, domain.RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected dev to not be org admin, got %v", err)
	}
	// ...and cannot touch a different project in the same org.
	def, _ := svc.DefaultProject(ctx, org)
	if err := svc.AuthorizeProject(ctx, dev.User.ID, org, def.ID, domain.RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected no access to default project, got %v", err)
	}
}

func TestAcceptInvitationWrongEmailForbidden(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	owner, _ := svc.Signup(ctx, "wo@example.com", "WO", "supersecret")
	inv, _ := svc.Invite(ctx, owner.User.ID, orgOf(t, svc, owner), "", "right@example.com", domain.RoleMember)

	other, _ := svc.Signup(ctx, "wrong@example.com", "Wrong", "supersecret")
	if _, err := svc.AcceptInvitation(ctx, other.User.ID, "wrong@example.com", inv.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden for email mismatch, got %v", err)
	}
}
