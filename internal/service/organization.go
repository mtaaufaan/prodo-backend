package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// organizationRepository -- interface didefinisikan di consumer, lihat §3.9.
// exec (db.Executor, S3-42) adalah transaksi request-scoped dari
// middleware.DBContextMiddleware -- organizations/workspaces kena RLS sejak
// S3-42, jadi WAJIB dipanggil dengan exec yang session variable-nya sudah
// disuntik (sama pola dengan WorkspaceRoleChecker/RBACService, S2-10/11).
type organizationRepository interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
	GetGroupID(ctx context.Context, exec db.Executor, orgID string) (string, error)
	Create(ctx context.Context, exec db.Executor, groupID, name, slug, orgDomain, defaultLanguage string, quotaBytes int64, retentionDays int, actorID, actorRole string) (*repository.Organization, error)
	Update(ctx context.Context, exec db.Executor, orgID, name, slug, orgDomain, actorID, actorRole string) error
	UpdateSettings(ctx context.Context, exec db.Executor, orgID, defaultLanguage, actorID, actorRole string) error
	UpdateStorageQuota(ctx context.Context, exec db.Executor, orgID string, quotaBytes int64, retentionDays int, actorID, actorRole string) error
	Deactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	Reactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	Delete(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	GetSummary(ctx context.Context, exec db.Executor, orgID string) (*repository.Summary, error)
	List(ctx context.Context, exec db.Executor, groupID string) ([]repository.Organization, int64, error)
	IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error)
}

// OrganizationService -- S3-02/03/04/05/06, US-007. Otorisasi Platform Admin
// (bypass penuh) ATAU Group Admin yang benar-benar di-assign ke grup target
// (group_admin_assignments, S3-38) ditegakkan DI SINI (bukan middleware) --
// beda dari RequireRole (workspace) karena POST /organizations membuat
// resource baru, jadi scoping-nya ke group_id dari request, bukan resource
// yang sudah ada. Lihat implementation_gaps.md IG-01.
type OrganizationService struct {
	repo organizationRepository
}

func NewOrganizationService(repo organizationRepository) *OrganizationService {
	return &OrganizationService{repo: repo}
}

// authorizeGroup menolak actor yang bukan Platform Admin dan bukan Group
// Admin dari groupID -- dipakai Create (groupID dari request body).
func (s *OrganizationService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.repo.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// IsGroupAdminOfGroup -- pass-through tipis ke repo (S3-20), dipakai
// GroupService untuk cek apakah actor pengelola grup target (selain jalur
// Platform Admin/Project Manager).
func (s *OrganizationService) IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error) {
	isGA, err := s.repo.IsGroupAdminOfGroup(ctx, exec, userID, groupID)
	if err != nil {
		return false, fmt.Errorf("service.IsGroupAdminOfGroup: %w", err)
	}
	return isGA, nil
}

// AuthorizeOrgAccess menolak actor yang bukan Platform Admin dan bukan Group
// Admin dari grup pemilik orgID -- dipakai Update/Deactivate/Delete/GetSummary
// (org sudah ada). Diekspor (bukan authorizeOrg lagi) supaya WorkspaceService
// (S3-09) bisa reuse pengecekan yang sama persis lewat interface
// orgAuthorizer -- workspace baru dibuat DI DALAM sebuah org, jadi otorisasi
// "siapa boleh menyentuh org ini" identik.
func (s *OrganizationService) AuthorizeOrgAccess(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	groupID, err := s.repo.GetGroupID(ctx, exec, orgID)
	if err != nil {
		return fmt.Errorf("service.AuthorizeOrgAccess: %w", err)
	}
	return s.authorizeGroup(ctx, exec, groupID, actorID, actorRole)
}

// CreateOrganization membuat organisasi baru (S3-02). name/slug wajib diisi;
// slug divalidasi format DI HANDLER (validator.IsValidSlug), bukan di sini.
// domain/defaultLanguage/quotaBytes/retentionDays ditambahkan S4G-31 (Track
// S4G, desain "GA Add Organization.dc.html") -- lihat komentar
// OrganizationRepository.Create soal reuse validasi UpdateStorageQuota.
func (s *OrganizationService) CreateOrganization(ctx context.Context, exec db.Executor, groupID, name, slug, orgDomain, defaultLanguage string, quotaBytes int64, retentionDays int, actorID, actorRole string) (*repository.Organization, error) {
	if groupID == "" || name == "" || slug == "" || quotaBytes <= 0 {
		return nil, fmt.Errorf("service.CreateOrganization: %w", domain.ErrInvalidInput)
	}
	if orgDomain != "" && !orgDomainPattern.MatchString(orgDomain) {
		return nil, fmt.Errorf("service.CreateOrganization: %w", domain.ErrInvalidInput)
	}
	if !validOrgLanguages[defaultLanguage] {
		return nil, fmt.Errorf("service.CreateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}

	org, err := s.repo.Create(ctx, exec, groupID, name, slug, orgDomain, defaultLanguage, quotaBytes, retentionDays, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateOrganization: %w", err)
	}
	return org, nil
}

// orgDomainPattern -- format domain email resmi (S4G-02, Track S4G, desain
// "GA Organizations.dc.html"), sama regex dengan CHECK constraint DB.
var orgDomainPattern = regexp.MustCompile(`(?i)^[a-z0-9.-]+\.[a-z]{2,}$`)

// UpdateOrganization mengubah name/slug/domain organisasi existing (S3-03,
// domain ditambahkan S4G-02). orgDomain kosong ("") berarti dikosongkan
// (opsional) -- kalau diisi, WAJIB format domain valid.
func (s *OrganizationService) UpdateOrganization(ctx context.Context, exec db.Executor, orgID, name, slug, orgDomain, actorID, actorRole string) error {
	if orgID == "" || name == "" || slug == "" {
		return fmt.Errorf("service.UpdateOrganization: %w", domain.ErrInvalidInput)
	}
	if orgDomain != "" && !orgDomainPattern.MatchString(orgDomain) {
		return fmt.Errorf("service.UpdateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Update(ctx, exec, orgID, name, slug, orgDomain, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateOrganization: %w", err)
	}
	return nil
}

// validOrgLanguages -- org_language enum (S3-29), DATABASE_SCHEMA.md.
var validOrgLanguages = map[string]bool{"id": true, "en": true}

// UpdateSettings mengubah default_language organisasi (S3-30, US-010).
func (s *OrganizationService) UpdateSettings(ctx context.Context, exec db.Executor, orgID, defaultLanguage, actorID, actorRole string) error {
	if orgID == "" || !validOrgLanguages[defaultLanguage] {
		return fmt.Errorf("service.UpdateSettings: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.UpdateSettings(ctx, exec, orgID, defaultLanguage, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateSettings: %w", err)
	}
	return nil
}

// UpdateStorageQuota mengubah kuota storage + retensi organisasi (S3-34/
// US-011, retensi ditambah S4G-03/Track S4G) -- divalidasi tidak melebihi
// storage_max_bytes dan retensi 30-365 hari di repository.
func (s *OrganizationService) UpdateStorageQuota(ctx context.Context, exec db.Executor, orgID string, quotaBytes int64, retentionDays int, actorID, actorRole string) error {
	if orgID == "" || quotaBytes < 0 {
		return fmt.Errorf("service.UpdateStorageQuota: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.UpdateStorageQuota(ctx, exec, orgID, quotaBytes, retentionDays, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateStorageQuota: %w", err)
	}
	return nil
}

// DeactivateOrganization menonaktifkan organisasi (S3-04) -- US-007 AC:
// seluruh akses member diblokir, data tetap tersimpan (soft, bukan DELETE).
func (s *OrganizationService) DeactivateOrganization(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	if orgID == "" {
		return fmt.Errorf("service.DeactivateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Deactivate(ctx, exec, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.DeactivateOrganization: %w", err)
	}
	return nil
}

// ReactivateOrganization membatalkan deactivate (kebalikan DeactivateOrganization).
func (s *OrganizationService) ReactivateOrganization(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	if orgID == "" {
		return fmt.Errorf("service.ReactivateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Reactivate(ctx, exec, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.ReactivateOrganization: %w", err)
	}
	return nil
}

// ListOrganizations mengembalikan organisasi yang terlihat oleh actor,
// plus plafon storage grup (lihat repository.List). groupID kosong berarti
// tidak ada filter tambahan -- scoping sepenuhnya lewat RLS, sama seperti
// sebelumnya (dipakai Platform Admin lintas grup). groupID diisi (S4G-32,
// Track S4G, group switcher -- Group Admin yang mengelola >1 grup,
// DATABASE_SCHEMA.md §5.6) WAJIB divalidasi actor benar-benar berwenang
// atas grup itu di sini -- RLS sendiri cuma menjamin "grup APA SAJA yang
// dikelola", bukan "grup yang sedang aktif dipilih actor".
func (s *OrganizationService) ListOrganizations(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) ([]repository.Organization, int64, error) {
	if groupID != "" {
		if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
			return nil, 0, err
		}
	}
	orgs, ceilingBytes, err := s.repo.List(ctx, exec, groupID)
	if err != nil {
		return nil, 0, fmt.Errorf("service.ListOrganizations: %w", err)
	}
	return orgs, ceilingBytes, nil
}

// BulkUpdateStorageAllocation menyetel storage_quota_bytes SEKALIGUS untuk
// banyak organisasi dalam satu grup (S4G-07, Track S4G, desain
// "GA Storage Quota.dc.html" modal "Atur Alokasi Kuota"). Validasi
// SELURUH batch (per-org: wajib diisi, <= max, >= terpakai; total <=
// plafon grup) dikumpulkan penuh SEBELUM ada satu pun ditulis -- beda dari
// memanggil PUT /organizations/:id/storage-quota N kali terpisah, yang
// bisa menulis sebagian lalu gagal di tengah. Penulisan sungguhan reuse
// PENUH repo.UpdateStorageQuota (retention_days org itu TIDAK diubah, cuma
// kuota) -- diurutkan PENURUNAN dulu baru KENAIKAN supaya validasi ceiling
// gabungan di dalam UpdateStorageQuota (yang menjumlah kuota org LAIN saat
// itu) tidak pernah false-positive di tengah batch (lihat pembuktian di
// commit message/PR: total akhir sudah divalidasi <= cap di sini, urutan
// turun-dulu menjamin total berjalan monoton tidak pernah melebihi total
// akhir).
func (s *OrganizationService) BulkUpdateStorageAllocation(ctx context.Context, exec db.Executor, groupID string, allocations map[string]int64, actorID, actorRole string) error {
	if groupID == "" || len(allocations) == 0 {
		return fmt.Errorf("service.BulkUpdateStorageAllocation: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return err
	}

	orgs, ceilingBytes, err := s.repo.List(ctx, exec, groupID)
	if err != nil {
		return fmt.Errorf("service.BulkUpdateStorageAllocation: %w", err)
	}
	byID := make(map[string]*repository.Organization, len(orgs))
	for i := range orgs {
		byID[orgs[i].ID] = &orgs[i]
	}

	validationErrors := map[string]string{}
	var totalBytes int64
	type change struct {
		orgID    string
		newBytes int64
	}
	var changes []change
	for orgID, newBytes := range allocations {
		org, ok := byID[orgID]
		if !ok {
			validationErrors[orgID] = "organisasi tidak ditemukan dalam grup ini"
			continue
		}
		if newBytes <= 0 {
			validationErrors[orgID] = "alokasi wajib diisi"
			continue
		}
		if newBytes > org.StorageMaxBytes {
			validationErrors[orgID] = fmt.Sprintf("melebihi batas maksimum Platform Admin (%d GB)", org.StorageMaxBytes/(1024*1024*1024))
			continue
		}
		if newBytes < org.StorageUsedBytes {
			validationErrors[orgID] = fmt.Sprintf("di bawah pemakaian saat ini (%.1f GB)", float64(org.StorageUsedBytes)/(1024*1024*1024))
			continue
		}
		totalBytes += newBytes
		if newBytes != org.StorageQuotaBytes {
			changes = append(changes, change{orgID: orgID, newBytes: newBytes})
		}
	}
	// Org dalam grup yang TIDAK disebut di request tetap dihitung dengan
	// nilai lama (request boleh parsial -- cuma baris yang diubah user).
	for id, org := range byID {
		if _, touched := allocations[id]; !touched {
			totalBytes += org.StorageQuotaBytes
		}
	}
	if ceilingBytes > 0 && totalBytes > ceilingBytes {
		validationErrors["_total"] = fmt.Sprintf("total alokasi %d GB melebihi plafon grup %d GB", totalBytes/(1024*1024*1024), ceilingBytes/(1024*1024*1024))
	}
	if len(validationErrors) > 0 {
		return fmt.Errorf("service.BulkUpdateStorageAllocation: %w", &domain.BulkAllocationError{Errors: validationErrors})
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].newBytes-byID[changes[i].orgID].StorageQuotaBytes < changes[j].newBytes-byID[changes[j].orgID].StorageQuotaBytes
	})
	for _, c := range changes {
		org := byID[c.orgID]
		if err := s.repo.UpdateStorageQuota(ctx, exec, c.orgID, c.newBytes, org.RetentionDays, actorID, actorRole); err != nil {
			return fmt.Errorf("service.BulkUpdateStorageAllocation: org %s: %w", c.orgID, err)
		}
	}
	return nil
}

// IsActive -- pass-through tipis ke repo (S4G-04, Track S4G), dipakai
// WorkspaceService.MoveWorkspace untuk guard org tujuan pindah workspace.
func (s *OrganizationService) IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error) {
	active, err := s.repo.IsActive(ctx, exec, orgID)
	if err != nil {
		return false, fmt.Errorf("service.IsActive: %w", err)
	}
	return active, nil
}

// DeleteOrganization menghapus organisasi permanen (S3-05) -- ditolak kalau
// masih ada workspace aktif di dalamnya (domain.ErrOrganizationHasWorkspaces).
func (s *OrganizationService) DeleteOrganization(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	if orgID == "" {
		return fmt.Errorf("service.DeleteOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, exec, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.DeleteOrganization: %w", err)
	}
	return nil
}

// GetSummary mengembalikan ringkasan dashboard organisasi (S3-06) --
// terbuka untuk actor yang sama seperti Update/Deactivate (GA pengelola
// grup pemilik org, atau Platform Admin).
func (s *OrganizationService) GetSummary(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) (*repository.Summary, error) {
	if orgID == "" {
		return nil, fmt.Errorf("service.GetSummary: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return nil, err
	}

	summary, err := s.repo.GetSummary(ctx, exec, orgID)
	if err != nil {
		return nil, fmt.Errorf("service.GetSummary: %w", err)
	}
	return summary, nil
}
