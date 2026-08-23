package service

import (
	"context"
	"fmt"

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
	Create(ctx context.Context, exec db.Executor, groupID, name, slug, actorID, actorRole string) (*repository.Organization, error)
	Update(ctx context.Context, exec db.Executor, orgID, name, slug, actorID, actorRole string) error
	Deactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	Reactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	Delete(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error
	GetSummary(ctx context.Context, exec db.Executor, orgID string) (*repository.Summary, error)
	List(ctx context.Context, exec db.Executor) ([]repository.Organization, error)
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
func (s *OrganizationService) CreateOrganization(ctx context.Context, exec db.Executor, groupID, name, slug, actorID, actorRole string) (*repository.Organization, error) {
	if groupID == "" || name == "" || slug == "" {
		return nil, fmt.Errorf("service.CreateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}

	org, err := s.repo.Create(ctx, exec, groupID, name, slug, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateOrganization: %w", err)
	}
	return org, nil
}

// UpdateOrganization mengubah name/slug organisasi existing (S3-03).
func (s *OrganizationService) UpdateOrganization(ctx context.Context, exec db.Executor, orgID, name, slug, actorID, actorRole string) error {
	if orgID == "" || name == "" || slug == "" {
		return fmt.Errorf("service.UpdateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.AuthorizeOrgAccess(ctx, exec, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Update(ctx, exec, orgID, name, slug, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateOrganization: %w", err)
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

// ListOrganizations mengembalikan organisasi yang terlihat oleh actor --
// scoping sepenuhnya lewat RLS (lihat repository.List), tidak ada
// pengecekan tambahan di sini.
func (s *OrganizationService) ListOrganizations(ctx context.Context, exec db.Executor) ([]repository.Organization, error) {
	orgs, err := s.repo.List(ctx, exec)
	if err != nil {
		return nil, fmt.Errorf("service.ListOrganizations: %w", err)
	}
	return orgs, nil
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
