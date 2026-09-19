// Package service -- WorkspaceAuditService (S4W-16/17, US-058, desain
// "AW Audit Trail.dc.html"). Lihat komentar package repository untuk kenapa
// ini baca langsung dari `audit_logs` (pola sama GroupAuditService, IG-45)
// dan kenapa resolusi nama target TIDAK PERNAH lewat live JOIN ke entitas
// yang bisa hilang.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// workspaceAuditRepository -- interface didefinisikan di consumer, §3.9.
type workspaceAuditRepository interface {
	List(ctx context.Context, exec db.Executor, f repository.WorkspaceAuditLogFilter) ([]repository.WorkspaceAuditLogEntry, int, error)
	ListActors(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.WorkspaceAuditActor, error)
}

// workspaceAuditRuleExecutions -- reuse RuleRepository.ListExecutions (S4W-11)
// supaya "Log Eksekusi" Rule Automation ikut tergabung di feed Audit Trail
// Workspace, sesuai instruksi kickoff "gabung feed rule execution" --
// SUMBER TERPISAH (automation_rule_executions), bukan baris audit_logs.
type workspaceAuditRuleExecutions interface {
	ListExecutions(ctx context.Context, exec db.Executor, workspaceID, statusFilter string) ([]repository.RuleExecution, error)
}

// WorkspaceAuditCSVExportLimit -- sama batas GroupAuditService.CSVExportLimit.
const WorkspaceAuditCSVExportLimit = 2000

type WorkspaceAuditService struct {
	repo  workspaceAuditRepository
	rules workspaceAuditRuleExecutions
	rbac  sprintWorkspaceRoleChecker
}

func NewWorkspaceAuditService(repo workspaceAuditRepository, rules workspaceAuditRuleExecutions, rbac sprintWorkspaceRoleChecker) *WorkspaceAuditService {
	return &WorkspaceAuditService{repo: repo, rules: rules, rbac: rbac}
}

// authorizeWorkspace -- AW-only, PERSIS pola RuleService/WebhookService/
// TaskAttachmentService/PerformanceService.
func (s *WorkspaceAuditService) authorizeWorkspace(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorizeWorkspace: %w", err)
	}
	if role != "admin_workspace" {
		return fmt.Errorf("service.authorizeWorkspace: %w", domain.ErrForbidden)
	}
	return nil
}

// List -- filter kosong berarti tidak difilter, actionType harus salah satu
// dari CREATE/UPDATE/DELETE/ACCESS kalau diisi. TIDAK menyertakan feed rule
// execution -- itu HANYA relevan untuk tampilan gabungan (lihat
// RuleExecutionFeed), List/ExportCSV murni audit_logs supaya paginasi tetap
// sederhana dan konsisten dengan GroupAuditService.
func (s *WorkspaceAuditService) List(ctx context.Context, exec db.Executor, workspaceID, actorID, actorFilterID, actionType string, days, limit, offset int, actorRole string) ([]repository.WorkspaceAuditLogEntry, int, error) {
	if workspaceID == "" {
		return nil, 0, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	entries, total, err := s.repo.List(ctx, exec, repository.WorkspaceAuditLogFilter{
		WorkspaceID: workspaceID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("service.List: %w", err)
	}
	return entries, total, nil
}

// ExportCSV -- sinkron, pola PERSIS GroupAuditService.ExportCSV.
func (s *WorkspaceAuditService) ExportCSV(ctx context.Context, exec db.Executor, workspaceID, actorID, actorFilterID, actionType string, days int, actorRole string) ([]repository.WorkspaceAuditLogEntry, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ExportCSV: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	entries, _, err := s.repo.List(ctx, exec, repository.WorkspaceAuditLogFilter{
		WorkspaceID: workspaceID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: WorkspaceAuditCSVExportLimit, Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("service.ExportCSV: %w", err)
	}
	return entries, nil
}

// ListActors -- opsi dropdown "AKTOR".
func (s *WorkspaceAuditService) ListActors(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) ([]repository.WorkspaceAuditActor, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListActors: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	actors, err := s.repo.ListActors(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListActors: %w", err)
	}
	return actors, nil
}

// RuleExecutions -- "gabung feed rule execution" (kickoff S4W-16). Dipanggil
// terpisah dari List, digabung di FE (pola sama desain "AW Audit
// Trail.dc.html": this.state.audit.concat(fromRules)) -- backend tidak
// perlu menyatukan dua bentuk baris yang berbeda struktur di satu response.
func (s *WorkspaceAuditService) RuleExecutions(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) ([]repository.RuleExecution, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.RuleExecutions: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWorkspace(ctx, exec, workspaceID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.rules.ListExecutions(ctx, exec, workspaceID, "")
	if err != nil {
		return nil, fmt.Errorf("service.RuleExecutions: %w", err)
	}
	return list, nil
}
