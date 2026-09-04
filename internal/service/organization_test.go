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

	listResult      []repository.Organization
	listErr         error
	listCalledGroup string
	listCeiling     int64

	quotaUpdates []quotaUpdate

	isActiveResult bool
	isActiveErr    error
}

type quotaUpdate struct {
	orgID      string
	quotaBytes int64
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

func (f *fakeOrganizationRepo) Create(_ context.Context, _ db.Executor, groupID, name, slug, orgDomain, defaultLanguage string, quotaBytes int64, retentionDays int, _, _ string) (*repository.Organization, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.createdOrg = &repository.Organization{ID: "org-new", GroupID: groupID, Name: name, Slug: slug, Domain: orgDomain, DefaultLanguage: defaultLanguage, StorageQuotaBytes: quotaBytes, RetentionDays: retentionDays}
	return f.createdOrg, nil
}

func (f *fakeOrganizationRepo) Update(_ context.Context, _ db.Executor, _, _, _, _, _, _ string) error {
	return f.updateErr
}

func (f *fakeOrganizationRepo) UpdateSettings(_ context.Context, _ db.Executor, _, _, _, _ string) error {
	return f.settingsErr
}

func (f *fakeOrganizationRepo) UpdateStorageQuota(_ context.Context, _ db.Executor, orgID string, quotaBytes int64, _ int, _, _ string) error {
	if f.quotaErr != nil {
		return f.quotaErr
	}
	f.quotaUpdates = append(f.quotaUpdates, quotaUpdate{orgID: orgID, quotaBytes: quotaBytes})
	return nil
}

func (f *fakeOrganizationRepo) IsActive(_ context.Context, _ db.Executor, _ string) (bool, error) {
	return f.isActiveResult, f.isActiveErr
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

func (f *fakeOrganizationRepo) List(_ context.Context, _ db.Executor, groupID string) ([]repository.Organization, int64, error) {
	f.listCalledGroup = groupID
	return f.listResult, f.listCeiling, f.listErr
}

func (f *fakeOrganizationRepo) GetSummary(_ context.Context, _ db.Executor, _ string) (*repository.Summary, error) {
	return f.summaryResult, f.summaryErr
}

func TestOrganizationService_CreateOrganization_PlatformAdminBypass(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	org, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "", "id", 1_000_000_000, 90, "pa-1", "platform_admin")
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

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "", "id", 1_000_000_000, 90, "ga-1", "group_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_CreateOrganization_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{}}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "", "id", 1_000_000_000, 90, "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MemberForbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "", "id", 1_000_000_000, 90, "member-1", "member")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_CreateOrganization_MissingFields(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	_, err := svc.CreateOrganization(context.Background(), nil, "", "Acme", "acme", "", "id", 1_000_000_000, 90, "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_CreateOrganization_InvalidLanguage(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "", "fr", 1_000_000_000, 90, "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_CreateOrganization_InvalidDomain(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	_, err := svc.CreateOrganization(context.Background(), nil, "group-1", "Acme", "acme", "not-a-domain", "id", 1_000_000_000, 90, "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_ListOrganizations_NoGroupFilter_PassesThrough(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	if _, _, err := svc.ListOrganizations(context.Background(), nil, "", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listCalledGroup != "" {
		t.Errorf("listCalledGroup = %q, want empty (no filter)", repo.listCalledGroup)
	}
}

func TestOrganizationService_ListOrganizations_GroupAdminOfGroup_Allowed(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{"ga-1:group-1": true}}
	svc := NewOrganizationService(repo)

	if _, _, err := svc.ListOrganizations(context.Background(), nil, "group-1", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listCalledGroup != "group-1" {
		t.Errorf("listCalledGroup = %q, want %q", repo.listCalledGroup, "group-1")
	}
}

func TestOrganizationService_ListOrganizations_GroupAdminNotOfGroup_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{}}
	svc := NewOrganizationService(repo)

	_, _, err := svc.ListOrganizations(context.Background(), nil, "group-1", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_ListOrganizations_PlatformAdminBypassesGroupCheck(t *testing.T) {
	repo := &fakeOrganizationRepo{}
	svc := NewOrganizationService(repo)

	if _, _, err := svc.ListOrganizations(context.Background(), nil, "group-1", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listCalledGroup != "group-1" {
		t.Errorf("listCalledGroup = %q, want %q", repo.listCalledGroup, "group-1")
	}
}

func TestOrganizationService_UpdateOrganization_GroupAdminOfOrgsGroup(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup: map[string]string{"org-1": "group-1"},
		gaGroups: map[string]bool{"ga-1:group-1": true},
	}
	svc := NewOrganizationService(repo)

	if err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme Baru", "acme-baru", "", "ga-1", "group_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrganizationService_UpdateOrganization_GroupAdminOfOtherGroup_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{
		orgGroup: map[string]string{"org-1": "group-1"},
		gaGroups: map[string]bool{"ga-2:group-2": true},
	}
	svc := NewOrganizationService(repo)

	err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme Baru", "acme-baru", "", "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

// Untuk Group Admin, org-not-found ketahuan lebih awal lewat authorizeOrg's
// GetGroupID (S3-03).
func TestOrganizationService_UpdateOrganization_OrgNotFound_GroupAdmin(t *testing.T) {
	svc := NewOrganizationService(&fakeOrganizationRepo{})

	err := svc.UpdateOrganization(context.Background(), nil, "org-missing", "Acme", "acme", "", "ga-1", "group_admin")
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

	err := svc.UpdateOrganization(context.Background(), nil, "org-missing", "Acme", "acme", "", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrOrganizationNotFound) {
		t.Errorf("err = %v, want wrapped domain.ErrOrganizationNotFound", err)
	}
}

func TestOrganizationService_UpdateOrganization_InvalidDomain(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme", "acme", "not-a-domain", "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapped domain.ErrInvalidInput", err)
	}
}

func TestOrganizationService_UpdateOrganization_ValidDomain(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	if err := svc.UpdateOrganization(context.Background(), nil, "org-1", "Acme", "acme", "acme.co.id", "pa-1", "platform_admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	err := svc.UpdateStorageQuota(context.Background(), nil, "org-1", 999999999999, 90, "pa-1", "platform_admin")
	if !errors.Is(err, domain.ErrStorageQuotaExceedsMax) {
		t.Errorf("err = %v, want wrapped domain.ErrStorageQuotaExceedsMax", err)
	}
}

func TestOrganizationService_UpdateStorageQuota_GroupAdminNotAssigned_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}}
	svc := NewOrganizationService(repo)

	err := svc.UpdateStorageQuota(context.Background(), nil, "org-1", 1024, 90, "ga-1", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_UpdateStorageQuota_RetentionOutOfRange(t *testing.T) {
	repo := &fakeOrganizationRepo{orgGroup: map[string]string{"org-1": "group-1"}, quotaErr: &domain.RetentionOutOfRangeError{MinDays: 30, MaxDays: 90, TierName: "starter"}}
	svc := NewOrganizationService(repo)

	err := svc.UpdateStorageQuota(context.Background(), nil, "org-1", 1024, 400, "pa-1", "platform_admin")
	var retentionErr *domain.RetentionOutOfRangeError
	if !errors.As(err, &retentionErr) {
		t.Errorf("err = %v, want wrapped *domain.RetentionOutOfRangeError", err)
	}
}

const gbBytes = 1024 * 1024 * 1024

func TestOrganizationService_BulkUpdateStorageAllocation_AppliesDecreaseBeforeIncrease(t *testing.T) {
	repo := &fakeOrganizationRepo{
		listCeiling: 100 * gbBytes,
		listResult: []repository.Organization{
			{ID: "org-a", GroupID: "group-1", StorageQuotaBytes: 60 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 5 * gbBytes, RetentionDays: 90},
			{ID: "org-b", GroupID: "group-1", StorageQuotaBytes: 30 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 5 * gbBytes, RetentionDays: 90},
			{ID: "org-c", GroupID: "group-1", StorageQuotaBytes: 10 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 5 * gbBytes, RetentionDays: 90},
		},
	}
	svc := NewOrganizationService(repo)

	// org-a turun 60->20, org-b naik 30->70, org-c tidak disebut (tetap
	// 10) -- total akhir 20+70+10=100, pas di plafon.
	err := svc.BulkUpdateStorageAllocation(context.Background(), nil, "group-1",
		map[string]int64{"org-a": 20 * gbBytes, "org-b": 70 * gbBytes}, "pa-1", "platform_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.quotaUpdates) != 2 {
		t.Fatalf("quotaUpdates = %+v, want 2 updates", repo.quotaUpdates)
	}
	if repo.quotaUpdates[0].orgID != "org-a" || repo.quotaUpdates[1].orgID != "org-b" {
		t.Errorf("quotaUpdates order = %+v, want org-a (turun) sebelum org-b (naik)", repo.quotaUpdates)
	}
}

func TestOrganizationService_BulkUpdateStorageAllocation_BelowUsed(t *testing.T) {
	repo := &fakeOrganizationRepo{
		listCeiling: 100 * gbBytes,
		listResult: []repository.Organization{
			{ID: "org-a", GroupID: "group-1", StorageQuotaBytes: 60 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 50 * gbBytes, RetentionDays: 90},
		},
	}
	svc := NewOrganizationService(repo)

	err := svc.BulkUpdateStorageAllocation(context.Background(), nil, "group-1",
		map[string]int64{"org-a": 10 * gbBytes}, "pa-1", "platform_admin")
	var bulkErr *domain.BulkAllocationError
	if !errors.As(err, &bulkErr) {
		t.Fatalf("err = %v, want wrapped *domain.BulkAllocationError", err)
	}
	if _, ok := bulkErr.Errors["org-a"]; !ok {
		t.Errorf("bulkErr.Errors = %+v, want key org-a", bulkErr.Errors)
	}
	if len(repo.quotaUpdates) != 0 {
		t.Errorf("quotaUpdates = %+v, want no writes on validation failure", repo.quotaUpdates)
	}
}

func TestOrganizationService_BulkUpdateStorageAllocation_ExceedsCeiling_NoPartialWrite(t *testing.T) {
	repo := &fakeOrganizationRepo{
		listCeiling: 50 * gbBytes,
		listResult: []repository.Organization{
			{ID: "org-a", GroupID: "group-1", StorageQuotaBytes: 20 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 5 * gbBytes, RetentionDays: 90},
			{ID: "org-b", GroupID: "group-1", StorageQuotaBytes: 20 * gbBytes, StorageMaxBytes: 100 * gbBytes, StorageUsedBytes: 5 * gbBytes, RetentionDays: 90},
		},
	}
	svc := NewOrganizationService(repo)

	// org-a naik ke 40, org-b tetap 20 -> total 60 > plafon 50.
	err := svc.BulkUpdateStorageAllocation(context.Background(), nil, "group-1",
		map[string]int64{"org-a": 40 * gbBytes}, "pa-1", "platform_admin")
	var bulkErr *domain.BulkAllocationError
	if !errors.As(err, &bulkErr) {
		t.Fatalf("err = %v, want wrapped *domain.BulkAllocationError", err)
	}
	if _, ok := bulkErr.Errors["_total"]; !ok {
		t.Errorf("bulkErr.Errors = %+v, want key _total", bulkErr.Errors)
	}
	if len(repo.quotaUpdates) != 0 {
		t.Errorf("quotaUpdates = %+v, want no writes when total exceeds ceiling", repo.quotaUpdates)
	}
}

func TestOrganizationService_BulkUpdateStorageAllocation_Forbidden(t *testing.T) {
	repo := &fakeOrganizationRepo{gaGroups: map[string]bool{}}
	svc := NewOrganizationService(repo)

	err := svc.BulkUpdateStorageAllocation(context.Background(), nil, "group-1",
		map[string]int64{"org-a": 10 * gbBytes}, "ga-2", "group_admin")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, want wrapped domain.ErrForbidden", err)
	}
}

func TestOrganizationService_IsActive_PassThrough(t *testing.T) {
	repo := &fakeOrganizationRepo{isActiveResult: true}
	svc := NewOrganizationService(repo)

	active, err := svc.IsActive(context.Background(), nil, "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active {
		t.Errorf("active = %v, want true", active)
	}
}
