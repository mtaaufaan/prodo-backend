// Package service -- CustomStatusService (Task Management Core Phase 1;
// require_start_confirmation toggle Phase 4, US-018b). Baca-saja selain
// toggle itu -- CRUD status custom penuh (AW/PM Custom Status) tetap scope
// terpisah (US-019/020), lihat komentar package repository.
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type customStatusRepository interface {
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error)
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
	SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool) error
}

type CustomStatusService struct {
	repo customStatusRepository
	rbac sprintWorkspaceRoleChecker
}

func NewCustomStatusService(repo customStatusRepository, rbac sprintWorkspaceRoleChecker) *CustomStatusService {
	return &CustomStatusService{repo: repo, rbac: rbac}
}

func (s *CustomStatusService) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForWorkspace: %w", err)
	}
	return list, nil
}

// SetRequireStartConfirmation -- PUT /statuses/:id (Phase 4, US-018b/S4-64) --
// hanya PM dan Admin Workspace dari workspace pemilik status ini (status
// selalu scope_type='workspace' di codebase ini, lihat komentar package).
func (s *CustomStatusService) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool, actorID, actorRole string) error {
	if statusID == "" {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrInvalidInput)
	}
	status, err := s.repo.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if actorRole != "platform_admin" && actorRole != "group_admin" {
		role, err := s.rbac.GetMemberRole(ctx, exec, status.ScopeID, actorID)
		if err != nil {
			return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
		}
		if role != "admin_workspace" && role != "project_manager" {
			return fmt.Errorf("service.SetRequireStartConfirmation: %w", domain.ErrForbidden)
		}
	}
	if err := s.repo.SetRequireStartConfirmation(ctx, exec, statusID, require); err != nil {
		return fmt.Errorf("service.SetRequireStartConfirmation: %w", err)
	}
	return nil
}
