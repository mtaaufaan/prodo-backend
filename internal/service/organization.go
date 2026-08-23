package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// organizationRepository -- interface didefinisikan di consumer, lihat §3.9.
type organizationRepository interface {
	IsGroupAdminOfGroup(ctx context.Context, userID, groupID string) (bool, error)
	GetGroupID(ctx context.Context, orgID string) (string, error)
	Create(ctx context.Context, groupID, name, slug, actorID, actorRole string) (*repository.Organization, error)
	Update(ctx context.Context, orgID, name, slug, actorID, actorRole string) error
	Deactivate(ctx context.Context, orgID, actorID, actorRole string) error
}

// OrganizationService -- S3-02/03/04, US-007. Otorisasi Platform Admin
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
func (s *OrganizationService) authorizeGroup(ctx context.Context, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.repo.IsGroupAdminOfGroup(ctx, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// authorizeOrg menolak actor yang bukan Platform Admin dan bukan Group Admin
// dari grup pemilik orgID -- dipakai Update/Deactivate (org sudah ada).
func (s *OrganizationService) authorizeOrg(ctx context.Context, orgID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	groupID, err := s.repo.GetGroupID(ctx, orgID)
	if err != nil {
		return fmt.Errorf("service.authorizeOrg: %w", err)
	}
	return s.authorizeGroup(ctx, groupID, actorID, actorRole)
}

// CreateOrganization membuat organisasi baru (S3-02). name/slug wajib diisi;
// slug divalidasi format DI HANDLER (validator.IsValidSlug), bukan di sini.
func (s *OrganizationService) CreateOrganization(ctx context.Context, groupID, name, slug, actorID, actorRole string) (*repository.Organization, error) {
	if groupID == "" || name == "" || slug == "" {
		return nil, fmt.Errorf("service.CreateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, groupID, actorID, actorRole); err != nil {
		return nil, err
	}

	org, err := s.repo.Create(ctx, groupID, name, slug, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.CreateOrganization: %w", err)
	}
	return org, nil
}

// UpdateOrganization mengubah name/slug organisasi existing (S3-03).
func (s *OrganizationService) UpdateOrganization(ctx context.Context, orgID, name, slug, actorID, actorRole string) error {
	if orgID == "" || name == "" || slug == "" {
		return fmt.Errorf("service.UpdateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeOrg(ctx, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Update(ctx, orgID, name, slug, actorID, actorRole); err != nil {
		return fmt.Errorf("service.UpdateOrganization: %w", err)
	}
	return nil
}

// DeactivateOrganization menonaktifkan organisasi (S3-04) -- US-007 AC:
// seluruh akses member diblokir, data tetap tersimpan (soft, bukan DELETE).
func (s *OrganizationService) DeactivateOrganization(ctx context.Context, orgID, actorID, actorRole string) error {
	if orgID == "" {
		return fmt.Errorf("service.DeactivateOrganization: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeOrg(ctx, orgID, actorID, actorRole); err != nil {
		return err
	}

	if err := s.repo.Deactivate(ctx, orgID, actorID, actorRole); err != nil {
		return fmt.Errorf("service.DeactivateOrganization: %w", err)
	}
	return nil
}
