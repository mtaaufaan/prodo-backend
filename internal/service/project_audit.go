// Package service -- ProjectAuditService (menu "Audit Trail" PM, IG-130). Cakupan
// satu PROJECT di atas `audit_logs`, memakai repository yang sama dengan Audit
// Trail Workspace (WorkspaceAuditRepository dengan filter ProjectID) supaya
// resolusi nama target, klasifikasi tipe, dan narasi identik. Desain
// "AW Audit Trail.dc.html" dipakai bersama oleh Admin Workspace dan Project
// Manager (Master UI User: showAudit untuk kedua role).
//
// Gerbang akses ada DI SINI (PM project ini / Admin Workspace / GA / PA): RLS
// audit_logs mengizinkan semua anggota workspace membaca baris workspace-nya,
// jadi pembatasan ke project dan ke role berwenang tidak boleh diserahkan ke RLS.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type projectAuditRepository interface {
	List(ctx context.Context, exec db.Executor, f *repository.WorkspaceAuditLogFilter) ([]repository.WorkspaceAuditLogEntry, int, error)
	ListProjectActors(ctx context.Context, exec db.Executor, projectID string) ([]repository.WorkspaceAuditActor, error)
	ProjectInfo(ctx context.Context, exec db.Executor, projectID string) (workspaceID, name string, err error)
	LogExport(ctx context.Context, exec db.Executor, actorID, actorRole, workspaceID, projectID, name string, rows int) error
}

type ProjectAuditService struct {
	repo  projectAuditRepository
	rules workspaceAuditRuleExecutions
	pm    performancePMChecker
	rbac  sprintWorkspaceRoleChecker
}

func NewProjectAuditService(repo projectAuditRepository, rules workspaceAuditRuleExecutions, pm performancePMChecker, rbac sprintWorkspaceRoleChecker) *ProjectAuditService {
	return &ProjectAuditService{repo: repo, rules: rules, pm: pm, rbac: rbac}
}

// authorize -- PM project ini, Admin Workspace, GA/PA. Mengembalikan workspace,
// nama project, dan role EFEKTIF untuk audit (rute tanpa RequireRole).
func (s *ProjectAuditService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (workspaceID, name, auditRole string, err error) {
	if projectID == "" {
		return "", "", "", fmt.Errorf("service.ProjectAudit: %w", domain.ErrInvalidInput)
	}
	workspaceID, name, err = s.repo.ProjectInfo(ctx, exec, projectID)
	if err != nil {
		return "", "", "", err
	}
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return workspaceID, name, actorRole, nil
	}
	isPM, err := s.pm.IsPM(ctx, exec, projectID, actorID)
	if err != nil {
		return "", "", "", fmt.Errorf("service.ProjectAudit: %w", err)
	}
	if isPM {
		return workspaceID, name, "project_manager", nil
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", "", "", fmt.Errorf("service.ProjectAudit: %w", err)
	}
	if role != "admin_workspace" {
		return "", "", "", fmt.Errorf("service.ProjectAudit: %w", domain.ErrForbidden)
	}
	return workspaceID, name, role, nil
}

// List -- pola PERSIS WorkspaceAuditService.List, cakupan project.
func (s *ProjectAuditService) List(ctx context.Context, exec db.Executor, projectID, actorID, actorFilterID, actionType string, days, limit, offset int, actorRole string) ([]repository.WorkspaceAuditLogEntry, int, error) {
	if _, _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	entries, total, err := s.repo.List(ctx, exec, &repository.WorkspaceAuditLogFilter{
		ProjectID: projectID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("service.ProjectAudit.List: %w", err)
	}
	return entries, total, nil
}

// ExportCSV -- sinkron, batas sama WorkspaceAuditCSVExportLimit; aksi ekspor
// dicatat di audit trail project (ACCESS).
func (s *ProjectAuditService) ExportCSV(ctx context.Context, exec db.Executor, projectID, actorID, actorFilterID, actionType string, days int, actorRole string) ([]repository.WorkspaceAuditLogEntry, error) {
	workspaceID, name, auditRole, err := s.authorize(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return nil, err
	}
	entries, _, err := s.repo.List(ctx, exec, &repository.WorkspaceAuditLogFilter{
		ProjectID: projectID, ActorID: actorFilterID, ActionType: actionType, Days: days, Limit: WorkspaceAuditCSVExportLimit, Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("service.ProjectAudit.ExportCSV: %w", err)
	}
	if err := s.repo.LogExport(ctx, exec, actorID, auditRole, workspaceID, projectID, name, len(entries)); err != nil {
		return nil, fmt.Errorf("service.ProjectAudit.ExportCSV: audit: %w", err)
	}
	return entries, nil
}

// ListActors -- opsi dropdown AKTOR.
func (s *ProjectAuditService) ListActors(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) ([]repository.WorkspaceAuditActor, error) {
	if _, _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	actors, err := s.repo.ListProjectActors(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.ProjectAudit.ListActors: %w", err)
	}
	return actors, nil
}

// RuleExecutions -- Log Eksekusi rule level PROJECT untuk digabung di FE.
func (s *ProjectAuditService) RuleExecutions(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) ([]repository.RuleExecution, error) {
	if _, _, _, err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	list, err := s.rules.ListExecutions(ctx, exec, "project", projectID, "")
	if err != nil {
		return nil, fmt.Errorf("service.ProjectAudit.RuleExecutions: %w", err)
	}
	return list, nil
}
