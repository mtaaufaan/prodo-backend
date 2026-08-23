package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeOrganizationRepo struct {
	gaGroups map[string]bool // "userID:groupID" -> is GA of that group
	orgGroup map[string]string // orgID -> groupID

	createdOrg *repository.Organization
	createErr  error
	updateErr  error
	deactivateErr error
}

func (f *fakeOrganizationRepo) IsGroupAdminOfGroup(_ context.Context, userID, groupID string) (bool, error) {
	return f.gaGroups[userID+":"+groupID], nil
}

func (f *fakeOrganizationRepo) GetGroupID(_ context.Context, orgID string) (string, error) {
	groupID, ok := f.orgGroup[orgID]
	if !ok {
		return "", domain.ErrOrganizationNotFound
	}
	return groupID, nil
}

func (f *fakeOrganizationRepo) Create(_ context.Context, groupID, name, slug, _, _ string) (*repository.Organization, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createdOrg = &repository.Organization{ID: "org-new", GroupID: groupID, Name: name, Slug: slug}
	return f.createdOrg, nil
}

func (f *fakeOrganizationRepo) Update(_ context.Context, _, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeOrganizationRepo) Deactivate(_ context.Context, _, _, _ string) error {
	return f.deactivateErr
}

func TestOrganizationService_CreateOrganization_PlatformAdminBypass(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	org, err := svc.CreateOrganization(context.Background(), "group-1", "Acme", "acme", "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if org.ID != "org-new" {
		t.Errorf("org = %+v, unexpected", org)
	}
}

func TestOrganizationService_CreateOrganization_GroupAdminAssigned(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{"ga-1:group-1": true}}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), "group-1", "Acme", "acme", "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_CreateOrganization_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{}}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), "group-1", "Acme", "acme", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MemberForbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), "group-1", "Acme", "acme", "member-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MissingFields(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	_, err := svc.CreateOrganization(context.Background(), "", "Acme", "acme", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_UpdateOrganization_GroupAdminOfOrgsGroup(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup: map[string]string{"org-1": "group-1"},
		gaGroups: map[string]bool{"ga-1:group-1": true},
	}
	svc := NewOrganizationService(repo)

	if err := svc.UpdateOrganization(context.Background(), "org-1", "Acme Baru", "acme-baru", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_UpdateOrganization_GroupAdminOfOtherGroup_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup: map[string]string{"org-1": "group-1"},
		gaGroups: map[string]bool{"ga-2:group-2": true},
	}
	svc := NewOrganizationService(repo)

	err := svc.UpdateOrganization(context.Background(), "org-1", "Acme Baru", "acme-baru", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

// Untuk Group Admin, org-not-found ketahuan lebih awal lewat authorizeOrg's
// GetGroupID (S3-03).
func TestOrganizationService_UpdateOrganization_OrgNotFound_GroupAdmin(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	err := svc.UpdateOrganization(context.Background(), "org-missing", "Acme", "acme", "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrOrganizationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationNotFound", err)
	}
}

// Untuk Platform Admin (bypass authorizeOrg, tidak pernah panggil
// GetGroupID), org-not-found baru ketahuan lewat repo.Update sendiri (0 baris
// ter-update) -- fake ini mensimulasikan itu langsung.
func TestOrganizationService_UpdateOrganization_OrgNotFound_PlatformAdmin(t *testing.T) {
	repo := &fakeOrganizationRepo{updateErr: domain.ErrOrganizationNotFound}
	svc := NewOrganizationService(repo)

	err := svc.UpdateOrganization(context.Background(), "org-missing", "Acme", "acme", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrOrganizationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationNotFound", err)
	}
}

func TestOrganizationService_DeactivateOrganization_PlatformAdminBypass(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	if err := svc.DeactivateOrganization(context.Background(), "org-1", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_DeactivateOrganization_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	err := svc.DeactivateOrganization(context.Background(), "org-1", "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}
