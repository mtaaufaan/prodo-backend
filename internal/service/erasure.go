package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

const erasureConfirmationPhrase = "KONFIRMASI"

// erasureRepository -- subset dibutuhkan service ini, pola sama
// interface-kecil-di-service dengan repository lain di file ini.
type erasureRepository interface {
	Create(ctx context.Context, userID, orgID, requestedBy, reason string) (string, error)
	List(ctx context.Context) ([]repository.ErasureRequest, error)
	HasSharedWorkspaceAdminRole(ctx context.Context, requesterID, requesterPlatformRole, targetUserID string) (bool, error)
	Execute(ctx context.Context, requestID, processedBy string) error
	Reject(ctx context.Context, requestID, processedBy string) error
}

// ErasureService -- S4P-29/30/31/32, US-060.
type ErasureService struct {
	repo erasureRepository
}

func NewErasureService(repo erasureRepository) *ErasureService {
	return &ErasureService{repo: repo}
}

// CreateRequest -- S4P-29. Otorisasi (bukan RBAC middleware -- endpoint ini
// TERBUKA untuk semua user terautentikasi, lihat handler): boleh mengajukan
// untuk diri sendiri, ATAU untuk user lain kalau requester adalah
// admin_workspace/project_manager di workspace yang sama dengan target
// (docs/security-compliance.md §6.2: "AW/PM membuat entri"), ATAU
// group_admin/platform_admin (otoritas administratif platform).
func (s *ErasureService) CreateRequest(ctx context.Context, requesterID, requesterPlatformRole, targetUserID, orgID, reason string) (string, error) {
	if targetUserID == "" {
		targetUserID = requesterID
	}
	if orgID == "" {
		return "", fmt.Errorf("service.CreateRequest: %w", domain.ErrInvalidInput)
	}

	if targetUserID != requesterID && requesterPlatformRole != "group_admin" && requesterPlatformRole != "platform_admin" {
		allowed, err := s.repo.HasSharedWorkspaceAdminRole(ctx, requesterID, requesterPlatformRole, targetUserID)
		if err != nil {
			return "", fmt.Errorf("service.CreateRequest: %w", err)
		}
		if !allowed {
			return "", fmt.Errorf("service.CreateRequest: %w", domain.ErrForbidden)
		}
	}

	id, err := s.repo.Create(ctx, targetUserID, orgID, requesterID, reason)
	if err != nil {
		return "", fmt.Errorf("service.CreateRequest: %w", err)
	}
	return id, nil
}

// List -- S4P-30, antrian Platform Admin.
func (s *ErasureService) List(ctx context.Context) ([]repository.ErasureRequest, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return rows, nil
}

// Execute -- S4P-31/32. confirmation harus persis "KONFIRMASI" (konfirmasi
// dua langkah untuk aksi irreversible, AC sprint_backlog.md S4P-31) --
// ditegakkan di sini, BUKAN cuma di FE, supaya panggilan API langsung
// (Bruno/skrip) tetap wajib lewat gerbang yang sama.
func (s *ErasureService) Execute(ctx context.Context, requestID, processedBy, confirmation string) error {
	if confirmation != erasureConfirmationPhrase {
		return fmt.Errorf("service.Execute: %w", domain.ErrErasureConfirmationRequired)
	}
	if err := s.repo.Execute(ctx, requestID, processedBy); err != nil {
		return fmt.Errorf("service.Execute: %w", err)
	}
	return nil
}

// Reject -- S4P-31 (tambahan, lihat komentar ErasureRepository.Reject).
func (s *ErasureService) Reject(ctx context.Context, requestID, processedBy string) error {
	if err := s.repo.Reject(ctx, requestID, processedBy); err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	return nil
}
