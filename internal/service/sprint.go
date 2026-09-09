// Package service -- SprintService (Task Management Core Phase 1, US-013).
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// sprintRepository -- interface didefinisikan di consumer, §3.9.
type sprintRepository interface {
	Create(ctx context.Context, exec db.Executor, projectID, name string, startDate, endDate *time.Time, createdBy string) (*repository.Sprint, error)
	List(ctx context.Context, exec db.Executor, projectID string) ([]repository.Sprint, error)
	Get(ctx context.Context, exec db.Executor, sprintID string) (*repository.Sprint, error)
	Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time) error
	DeactivateAllInProject(ctx context.Context, exec db.Executor, projectID string) error
	SetActive(ctx context.Context, exec db.Executor, sprintID string, active bool) error
	Delete(ctx context.Context, exec db.Executor, sprintID string) error
	UnassignIncompleteTasks(ctx context.Context, exec db.Executor, sprintID, doneStatusID string) error
	Summary(ctx context.Context, exec db.Executor, sprintID string) (totalStoryPoints, unestimatedCount int, err error)
}

// sprintProjectResolver -- reuse ProjectRepository.GetWorkspaceID.
type sprintProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
}

// sprintCustomStatuses -- reuse CustomStatusRepository, dipakai
// CompleteSprint untuk resolve status DONE (S4-09: task belum selesai
// dipindah ke backlog).
type sprintCustomStatuses interface {
	ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]repository.CustomStatus, error)
}

// sprintWorkspaceRoleChecker -- reuse RBACService.GetMemberRole.
type sprintWorkspaceRoleChecker interface {
	GetMemberRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error)
}

// sprintProjectRoleChecker -- reuse ProjectMemberRepository.GetRole.
type sprintProjectRoleChecker interface {
	GetRole(ctx context.Context, exec db.Executor, projectID, userID string) (string, bool, error)
}

type SprintService struct {
	repo         sprintRepository
	projects     sprintProjectResolver
	statuses     sprintCustomStatuses
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewSprintService(repo sprintRepository, projects sprintProjectResolver, statuses sprintCustomStatuses, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *SprintService {
	return &SprintService{repo: repo, projects: projects, statuses: statuses, rbac: rbac, projectRoles: projectRoles}
}

// authorize -- gate tulis: viewer/division_viewer DITOLAK, selebihnya
// (admin_workspace/project_manager/editor/approver, atau project-scoped
// editor/approver) boleh. RLS tetap jadi lapisan pertama (membership),
// ini menambah gate ROLE yang RLS sendiri sengaja tidak tegakkan
// (RLS_DESIGN.md §7.6: "role check di application layer").
func (s *SprintService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
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

func (s *SprintService) Create(ctx context.Context, exec db.Executor, projectID, name string, startDate, endDate *time.Time, actorID, actorRole string) (*repository.Sprint, error) {
	name = strings.TrimSpace(name)
	if projectID == "" || name == "" {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}
	sprint, err := s.repo.Create(ctx, exec, projectID, name, startDate, endDate, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return sprint, nil
}

func (s *SprintService) List(ctx context.Context, exec db.Executor, projectID string) ([]repository.Sprint, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

func (s *SprintService) Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time, actorID, actorRole string) error {
	name = strings.TrimSpace(name)
	if sprintID == "" || name == "" {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, exec, sprintID, name, startDate, endDate); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// StartSprint -- "hanya satu sprint aktif per project" (S4-08): deaktivasi
// SEMUA sprint lain di project ini dulu, baru aktifkan target.
func (s *SprintService) StartSprint(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.DeactivateAllInProject(ctx, exec, sprint.ProjectID); err != nil {
		return fmt.Errorf("service.StartSprint: %w", err)
	}
	if err := s.repo.SetActive(ctx, exec, sprintID, true); err != nil {
		return fmt.Errorf("service.StartSprint: %w", err)
	}
	return nil
}

// CompleteSprint -- S4-09: sprint dinonaktifkan, task yang belum berstatus
// DONE dipindah ke backlog (sprint_id NULL) -- lihat komentar
// SprintRepository.UnassignIncompleteTasks kenapa bukan otomatis ke sprint
// berikutnya.
func (s *SprintService) CompleteSprint(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole); err != nil {
		return err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, sprint.ProjectID)
	if err != nil {
		return fmt.Errorf("service.CompleteSprint: %w", err)
	}
	statuses, err := s.statuses.ListForWorkspace(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("service.CompleteSprint: %w", err)
	}
	var doneStatusID string
	for _, st := range statuses {
		if st.Name == "DONE" {
			doneStatusID = st.ID
			break
		}
	}
	if err := s.repo.SetActive(ctx, exec, sprintID, false); err != nil {
		return fmt.Errorf("service.CompleteSprint: %w", err)
	}
	if doneStatusID != "" {
		if err := s.repo.UnassignIncompleteTasks(ctx, exec, sprintID, doneStatusID); err != nil {
			return fmt.Errorf("service.CompleteSprint: %w", err)
		}
	}
	return nil
}

// Summary -- GET /sprints/:id/summary (Phase 4, US-018a/S4-59).
func (s *SprintService) Summary(ctx context.Context, exec db.Executor, sprintID string) (totalStoryPoints, unestimatedCount int, err error) {
	if sprintID == "" {
		return 0, 0, fmt.Errorf("service.Summary: %w", domain.ErrInvalidInput)
	}
	totalStoryPoints, unestimatedCount, err = s.repo.Summary(ctx, exec, sprintID)
	if err != nil {
		return 0, 0, fmt.Errorf("service.Summary: %w", err)
	}
	return totalStoryPoints, unestimatedCount, nil
}

func (s *SprintService) Delete(ctx context.Context, exec db.Executor, sprintID, actorID, actorRole string) error {
	sprint, err := s.repo.Get(ctx, exec, sprintID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, sprint.ProjectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, exec, sprintID); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
