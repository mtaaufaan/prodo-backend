package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// platformAuditRepository -- subset PlatformAuditRepository yang dibutuhkan
// service ini, sama pola interface-kecil-di-service dengan accountRepository
// (memudahkan fake di unit test).
type platformAuditRepository interface {
	List(ctx context.Context, f repository.PlatformAuditLogFilter) ([]repository.PlatformAuditLogEntry, int, error)
}

// PlatformAuditService -- S4P-22, US-071: baca platform_audit_logs untuk
// panel "Platform Audit Trail".
type PlatformAuditService struct {
	repo platformAuditRepository
}

func NewPlatformAuditService(repo platformAuditRepository) *PlatformAuditService {
	return &PlatformAuditService{repo: repo}
}

// defaultAuditLogPerPage/maxAuditLogPerPage -- sama pola dengan
// GroupAdminHandler.List (docs/coding-conventions.md §7.1).
const (
	defaultAuditLogPerPage = 50
	maxAuditLogPerPage     = 200
)

// ListAuditLogs menjaga limit/offset tetap dalam batas wajar sebelum
// diteruskan ke repository -- handler cuma mem-parsing query param mentah.
func (s *PlatformAuditService) ListAuditLogs(ctx context.Context, f repository.PlatformAuditLogFilter) ([]repository.PlatformAuditLogEntry, int, error) {
	if f.Limit <= 0 {
		f.Limit = defaultAuditLogPerPage
	}
	if f.Limit > maxAuditLogPerPage {
		f.Limit = maxAuditLogPerPage
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	entries, total, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, 0, fmt.Errorf("service.ListAuditLogs: %w", err)
	}
	return entries, total, nil
}
