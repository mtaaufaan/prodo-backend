package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type fakeOrganizationRepo struct {
	gaGroups map[string]bool   // "userID:groupID" -> is GA of that group
	orgGroup map[string]string // orgID -> groupID

	createdOrg    *repository.Organization
	createErr     error
	updateErr     error
	settingsErr   error
	quotaErr      error
	deactivateErr error
	reactivateErr error
	deleteErr     error

	summaryResult *repository.Summary
	summaryErr    error

	listResult []repository.Organization
	listErr    error
}

func (f *fakeOrganizationRepo) IsGroupAdminOfGroup(_ context.Context, _ db.Executor, userID, groupID string) (bool, error) {
	return f.gaGroups[userID+":"+groupID], nil
}

func (f *fakeOrganizationRepo) GetGroupID(_ context.Context, _ db.Executor, orgID string) (string, error) {
	groupID, ok := f.orgGroup[orgID]
	if !ok {
		return "", domain.ErrOrganizationNotFound
	}
	return groupID, nil
}

func (f *fakeOrganizationRepo) Create(_ context.Context, _ db.Executor, groupID, name, slug, _, _ string) (*repository.Organization, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createdOrg = &repository.Organization{ID: "org-new", GroupID: groupID, Name: name, Slug: slug}
	return f.createdOrg, nil
}

func (f *fakeOrganizationRepo) Update(_ context.Context, _ db.Executor, _, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeOrganizationRepo) UpdateSettings(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return f.settingsErr
}

func (f *fakeOrganizationRepo) UpdateStorageQuota(_ context.Context, _ db.Executor, _ string, _ int64, _, _ string) error {
	return f.quotaErr
}

func (f *fakeOrganizationRepo) Deactivate(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deactivateErr
}

func (f *fakeOrganizationRepo) Reactivate(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.reactivateErr
}

func (f *fakeOrganizationRepo) Delete(_ context.Context, _ db.Executor, _, _, _ string) error {
	return f.deleteErr
}

func (f *fakeOrganizationRepo) List(_ context.Context, _ db.Executor) ([]repository.Organization, error) {
	return f.listResult, f.listErr
}

func (f *fakeOrganizationRepo) GetSummary(_ context.Context, _ db.Executor, _ string) (*repository.Summary, error) {
	return f.summaryResult, f.summaryErr
}

func TestOrganizationService_CreateOrganization_PlatformAdminBypass(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	org, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "pa-1", "platform_admin")
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

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_CreateOrganization_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{}}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MemberForbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "member-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MissingFields(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	_, err := svc.CreateOrganization(context.Background(), nil, "", "Acme", "acme", "pa-1", "platform_admin")
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

	if err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme Baru", "acme-baru", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_UpdateOrganization_GroupAdminOfOtherGroup_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup: map[string]string{"org-1": "group-1"},
		gaGroups: map[string]bool{"ga-2:group-2": true},
	}
	svc := NewOrganizationService(repo)

	err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme Baru", "acme-baru", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

// Untuk Group Admin, org-not-found ketahuan lebih awal lewat authorizeOrg's
// GetGroupID (S3-03).
func TestOrganizationService_UpdateOrganization_OrgNotFound_GroupAdmin(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	err := svc.UpdateOrganization(context.Background(), nil, "org-missing", "Acme", "acme", "ga-1", "group_admin")
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

	err := svc.UpdateOrganization(context.Background(), nil, "org-missing", "Acme", "acme", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrOrganizationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationNotFound", err)
	}
}

func TestOrganizationService_DeactivateOrganization_PlatformAdminBypass(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	if err := svc.DeactivateOrganization(context.Background(), nil, "org-1", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_DeleteOrganization_HasWorkspaces(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup:  map[string]string{"org-1": "group-1"},
		deleteErr: domain.ErrOrganizationHasWorkspaces,
	}
	svc := NewOrganizationService(repo)

	err := svc.DeleteOrganization(context.Background(), nil, "org-1", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrOrganizationHasWorkspaces) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationHasWorkspaces", err)
	}
}

func TestOrganizationService_GetSummary_Success(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup:      map[string]string{"org-1": "group-1"},
		summaryResult: &repository.Summary{MemberCount: 5, WorkspaceCount: 2, StorageUsedByte: 1048576},
	}
	svc := NewOrganizationService(repo)

	summary, err := svc.GetSummary(context.Background(), nil, "org-1", "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.MemberCount != 5 || summary.WorkspaceCount != 2 {
		t.Errorf("summary = %+v, unexpected", summary)
	}
}

func TestOrganizationService_GetSummary_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	_, err := svc.GetSummary(context.Background(), nil, "org-1", "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_DeactivateOrganization_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	err := svc.DeactivateOrganization(context.Background(), nil, "org-1", "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_UpdateSettings_Success(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	if err := svc.UpdateSettings(context.Background(), nil, "org-1", "en", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_UpdateSettings_InvalidLanguage(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	err := svc.UpdateSettings(context.Background(), nil, "org-1", "fr", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_UpdateStorageQuota_ExceedsMax(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}, quotaErr: domain.ErrStorageQuotaExceedsMax}
	svc := NewOrganizationService(repo)

	err := svc.UpdateStorageQuota(context.Background(), nil, "org-1", 999999999999, "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrStorageQuotaExceedsMax) {
		t.Errorf("err = %v, want wrapped domain.ErrStorageQuotaExceedsMax", err)
	}
}

func TestOrganizationService_UpdateStorageQuota_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	err := svc.UpdateStorageQuota(context.Background(), nil, "org-1", 1024, "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}
