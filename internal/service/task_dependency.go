// Package service -- TaskDependencyService (Task Management Core Phase 3,
// US-018: Task Dependencies Finish-to-Start Hard-Block). Deteksi circular
// dependency dan enforcement HARD-BLOCK saat status berubah ada di
// TaskService.SetStatus (lihat task.go) -- service ini menangani CRUD
// dependency itu sendiri (list/add/remove).
package service

import (
	"context"
	"fmt"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// taskDependencyRepository -- interface didefinisikan di consumer, §3.9.
type taskDependencyRepository interface {
	ListPredecessors(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskDependency, error)
	ListSuccessors(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskDependency, error)
	WouldCreateCycle(ctx context.Context, exec db.Executor, predecessorID, successorID string) ([]string, error)
	Create(ctx context.Context, exec db.Executor, predecessorID, successorID string, createdBy *string) error
	Delete(ctx context.Context, exec db.Executor, predecessorID, successorID string) error
}

// taskDependencyTaskResolver -- reuse TaskRepository.GetProjectID/Get.
type taskDependencyTaskResolver interface {
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
	Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error)
}

type TaskDependencyService struct {
	repo         taskDependencyRepository
	tasks        taskDependencyTaskResolver
	projects     taskProjectResolver
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewTaskDependencyService(repo taskDependencyRepository, tasks taskDependencyTaskResolver, projects taskProjectResolver, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TaskDependencyService {
	return &TaskDependencyService{repo: repo, tasks: tasks, projects: projects, rbac: rbac, projectRoles: projectRoles}
}

// authorize -- identik TaskService.resolveRole tanpa nilai balik role
// (viewer/division_viewer ditolak) -- duplikasi kecil disengaja, pola sama
// tiap service Task Core.
func (s *TaskDependencyService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
		}
		return nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("service.authorize: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("service.authorize: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return fmt.Errorf("service.authorize: %w", domain.ErrForbidden)
	}
	return nil
}

// List menangani GET /tasks/:id/dependencies.
func (s *TaskDependencyService) List(ctx context.Context, exec db.Executor, taskID string) (predecessors, successors []repository.TaskDependency, err error) {
	if taskID == "" {
		return nil, nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	predecessors, err = s.repo.ListPredecessors(ctx, exec, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("service.List: %w", err)
	}
	successors, err = s.repo.ListSuccessors(ctx, exec, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("service.List: %w", err)
	}
	return predecessors, successors, nil
}

// Add menangani POST /tasks/:id/dependencies (US-018, S4-47/51) -- taskID
// jadi successor, predecessorTaskID jadi predecessor (task ini menunggu
// predecessorTaskID selesai duluan). Urutan cek: self-reference -> project
// sama -> circular -> insert.
func (s *TaskDependencyService) Add(ctx context.Context, exec db.Executor, taskID, predecessorTaskID, actorID, actorRole string) (*repository.TaskDependency, error) {
	if taskID == "" || predecessorTaskID == "" {
		return nil, fmt.Errorf("service.Add: %w", domain.ErrInvalidInput)
	}
	if taskID == predecessorTaskID {
		return nil, fmt.Errorf("service.Add: %w", domain.ErrDependencySelfReference)
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	predecessorProjectID, err := s.tasks.GetProjectID(ctx, exec, predecessorTaskID)
	if err != nil {
		return nil, err
	}
	if predecessorProjectID != projectID {
		return nil, fmt.Errorf("service.Add: %w", domain.ErrDependencyCrossProject)
	}

	cyclePath, err := s.repo.WouldCreateCycle(ctx, exec, predecessorTaskID, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.Add: %w", err)
	}
	if cyclePath != nil {
		codes, err := s.resolveTaskCodes(ctx, exec, cyclePath)
		if err != nil {
			return nil, fmt.Errorf("service.Add: %w", err)
		}
		return nil, fmt.Errorf("service.Add: %w", &domain.CircularDependencyError{CyclePath: codes})
	}

	if err := s.repo.Create(ctx, exec, predecessorTaskID, taskID, &actorID); err != nil {
		return nil, err
	}
	predecessors, err := s.repo.ListPredecessors(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.Add: %w", err)
	}
	for i := range predecessors {
		if predecessors[i].PredecessorID == predecessorTaskID {
			return &predecessors[i], nil
		}
	}
	return nil, fmt.Errorf("service.Add: dependency baru tidak ditemukan setelah insert")
}

// resolveTaskCodes -- terjemahkan urutan task_id (path lingkaran) jadi
// task_code untuk pesan CIRCULAR_DEPENDENCY yang lebih terbaca.
func (s *TaskDependencyService) resolveTaskCodes(ctx context.Context, exec db.Executor, taskIDs []string) ([]string, error) {
	codes := make([]string, len(taskIDs))
	for i, id := range taskIDs {
		t, err := s.tasks.Get(ctx, exec, id)
		if err != nil {
			return nil, err
		}
		if t.TaskCode != nil {
			codes[i] = *t.TaskCode
		} else {
			codes[i] = t.Title
		}
	}
	return codes, nil
}

// Remove menangani DELETE /tasks/:id/dependencies/:predecessorId.
func (s *TaskDependencyService) Remove(ctx context.Context, exec db.Executor, taskID, predecessorTaskID, actorID, actorRole string) error {
	if taskID == "" || predecessorTaskID == "" {
		return fmt.Errorf("service.Remove: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, exec, predecessorTaskID, taskID); err != nil {
		return err
	}
	return nil
}
