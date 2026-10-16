// Package service -- TaskChecklistItemService (SUB-TASK, "PM Task
// Detail.dc.html", IG-97 susulan). Baca (daftar) cukup RLS task_checklist_items_select
// (semua project member lihat checklist yang sama). TULIS (tambah/ubah/hapus)
// digerbangi role di service (S6E-08, IG-139) karena RLS _write meloloskan siapa pun
// anggota workspace: PM/AW/PA/GA boleh, Editor hanya pada task miliknya
// (ErrNotYourTask), role lain (Approver/Viewer/Division Viewer) ErrForbidden.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

type taskChecklistItemRepository interface {
	Create(ctx context.Context, exec db.Executor, taskID, title, createdBy string) (*repository.TaskChecklistItem, error)
	ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskChecklistItem, error)
	GetTaskID(ctx context.Context, exec db.Executor, itemID string) (string, error)
	Update(ctx context.Context, exec db.Executor, itemID string, title *string, isDone *bool) error
	Delete(ctx context.Context, exec db.Executor, itemID string) error
}

// checklistTaskResolver -- task -> project (reuse TaskRepository.GetProjectID) + kepemilikan (IsOwner).
type checklistTaskResolver interface {
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
	taskOwnerChecker
}

type TaskChecklistItemService struct {
	repo         taskChecklistItemRepository
	tasks        checklistTaskResolver
	projects     attachmentProjectResolver
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewTaskChecklistItemService(repo taskChecklistItemRepository, tasks checklistTaskResolver, projects attachmentProjectResolver, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TaskChecklistItemService {
	return &TaskChecklistItemService{repo: repo, tasks: tasks, projects: projects, rbac: rbac, projectRoles: projectRoles}
}

// authorizeWrite -- role efektif aktor pada task ini (project-scoped dulu, lalu workspace; sama
// dengan TaskService.resolveRole) lalu aturan tulis sub-task.
func (s *TaskChecklistItemService) authorizeWrite(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return fmt.Errorf("service.authorizeWrite: %w", err)
	}
	role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID)
	if err != nil || !found {
		workspaceID, werr := s.projects.GetWorkspaceID(ctx, exec, projectID)
		if werr != nil {
			return fmt.Errorf("service.authorizeWrite: %w", werr)
		}
		if role, err = s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID); err != nil {
			return fmt.Errorf("service.authorizeWrite: %w", err)
		}
	}
	return checkChecklistWrite(ctx, exec, s.tasks, role, taskID, actorID)
}

// checkChecklistWrite -- aturan murni (diuji tabel keputusan): "" = PA/GA.
func checkChecklistWrite(ctx context.Context, exec db.Executor, owners taskOwnerChecker, role, taskID, actorID string) error {
	switch role {
	case "", "admin_workspace", "project_manager":
		return nil
	case "editor":
		return requireOwnTask(ctx, exec, owners, role, taskID, actorID)
	}
	return fmt.Errorf("service.checkChecklistWrite: %w", domain.ErrForbidden)
}

func (s *TaskChecklistItemService) Create(ctx context.Context, exec db.Executor, taskID, title, actorID, actorRole string) (*repository.TaskChecklistItem, error) {
	title = strings.TrimSpace(title)
	if taskID == "" || title == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if err := s.authorizeWrite(ctx, exec, taskID, actorID, actorRole); err != nil {
		return nil, err
	}
	item, err := s.repo.Create(ctx, exec, taskID, title, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return item, nil
}

func (s *TaskChecklistItemService) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskChecklistItem, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListForTask: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.ListForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	return list, nil
}

// GetTaskID -- dipakai handler untuk invalidasi cache FE lewat taskID
// (route PATCH/DELETE item cuma py :itemId).
func (s *TaskChecklistItemService) GetTaskID(ctx context.Context, exec db.Executor, itemID string) (string, error) {
	return s.repo.GetTaskID(ctx, exec, itemID)
}

func (s *TaskChecklistItemService) Update(ctx context.Context, exec db.Executor, itemID string, title *string, isDone *bool, actorID, actorRole string) error {
	if itemID == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
		}
		title = &trimmed
	}
	taskID, err := s.repo.GetTaskID(ctx, exec, itemID)
	if err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	if err := s.authorizeWrite(ctx, exec, taskID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, exec, itemID, title, isDone); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

func (s *TaskChecklistItemService) Delete(ctx context.Context, exec db.Executor, itemID, actorID, actorRole string) error {
	if itemID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	taskID, err := s.repo.GetTaskID(ctx, exec, itemID)
	if err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	if err := s.authorizeWrite(ctx, exec, taskID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, exec, itemID); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
