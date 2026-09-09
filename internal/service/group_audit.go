// Package service -- GroupAuditService (Audit Trail, Track S4G, desain
// "GA Audit Trail.dc.html"). Lihat komentar package repository dan
// implementation_gaps.md IG-45 untuk kenapa ini baca langsung dari
// `audit_logs`, bukan tabel `group_audit_logs` terpisah.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// groupAuditRepository -- interface didefinisikan di consumer, §3.9.
type groupAuditRepository interface {
	List(ctx context.Context, exec db.Executor, f repository.GroupAuditLogFilter) ([]repository.GroupAuditLogEntry, int, error)
	ListActors(ctx context.Context, exec db.Executor, groupID string) ([]repository.GroupAuditActor, error)
}

// groupAuditAuthorizer -- reuse OrganizationRepository, pola sama
// RetentionService.
type groupAuditAuthorizer interface {
	IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error)
}

// CSVExportLimit -- AC desain tidak menyebut batas eksplisit untuk audit
// trail (beda dari Import Data 5000 baris) -- pakai batas sama Platform
// Audit Trail (S4P-22) supaya satu request tidak memicu full-table scan.
const CSVExportLimit = 2000

type GroupAuditService struct {
	repo groupAuditRepository
	orgs groupAuditAuthorizer
}

func NewGroupAuditService(repo groupAuditRepository, orgs groupAuditAuthorizer) *GroupAuditService {
	return &GroupAuditService{repo: repo, orgs: orgs}
}

func (s *GroupAuditService) authorizeGroup(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) error {
	if actorRole == "platform_admin" {
		return nil
	}
	isGA, err := s.orgs.IsGroupAdminOfGroup(ctx, exec, actorID, groupID)
	if err != nil {
		return fmt.Errorf("service.authorizeGroup: %w", err)
	}
	if !isGA {
		return fmt.Errorf("service.authorizeGroup: %w", domain.ErrForbidden)
	}
	return nil
}

// List -- filter kosong berarti tidak difilter, actionType harus salah satu
// dari CREATE/UPDATE/DELETE/ACCESS kalau diisi.
func (s *GroupAuditService) List(ctx context.Context, exec db.Executor, groupID, actorID, actorFilterID, actionType string, days, limit, offset int, actorRole string) ([]repository.GroupAuditLogEntry, int, error) {
	if groupID == "" {
		return nil, 0, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	entries, total, err := s.repo.List(ctx, exec, repository.GroupAuditLogFilter{
		GroupID: groupID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("service.List: %w", err)
	}
	return entries, total, nil
}

// ExportCSV -- sinkron (bukan email+link 72 jam seperti mockup desain,
// disederhanakan sama pola Platform Audit Trail S4P-22 -- entri audit
// murni teks/metadata kecil, tidak butuh job async+MinIO seperti Data
// Retention export yang aslinya untuk data besar).
func (s *GroupAuditService) ExportCSV(ctx context.Context, exec db.Executor, groupID, actorID, actorFilterID, actionType string, days int, actorRole string) ([]repository.GroupAuditLogEntry, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.ExportCSV: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	entries, _, err := s.repo.List(ctx, exec, repository.GroupAuditLogFilter{
		GroupID: groupID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: CSVExportLimit, Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("service.ExportCSV: %w", err)
	}
	return entries, nil
}

// ListActors -- opsi dropdown "AKTOR".
func (s *GroupAuditService) ListActors(ctx context.Context, exec db.Executor, groupID, actorID, actorRole string) ([]repository.GroupAuditActor, error) {
	if groupID == "" {
		return nil, fmt.Errorf("service.ListActors: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeGroup(ctx, exec, groupID, actorID, actorRole); err != nil {
		return nil, err
	}
	actors, err := s.repo.ListActors(ctx, exec, groupID)
	if err != nil {
		return nil, fmt.Errorf("service.ListActors: %w", err)
	}
	return actors, nil
}
