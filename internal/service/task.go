// Package service -- TaskService (Task Management Core Phase 1, US-014;
// PIC Handoff Phase 2, US-017; completeness+dependency hard-block Phase 3,
// US-017c/018; story point permission gate + status time tracking +
// regression Phase 4, US-018a/018b/018c). Lihat komentar migrasi
// 20260924090000_task_core_phase1.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

var validFibonacciSP = map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true}
var validTaskPriority = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

// taskRepository -- interface didefinisikan di consumer, §3.9.
type taskRepository interface {
	Create(ctx context.Context, exec db.Executor, projectID string, sprintID *string, statusID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, createdBy string, assigneeUserIDs []string) (*repository.Task, error)
	Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error)
	List(ctx context.Context, exec db.Executor, projectID string, f repository.TaskFilter) ([]repository.Task, error)
	Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string) error
	SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool) error
	SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness string) error
	SoftDelete(ctx context.Context, exec db.Executor, taskID string) error
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
}

// taskPicRepository -- reuse TaskPicRepository (Phase 2). Interface
// didefinisikan di consumer -- cuma method yang dipakai TaskService.
type taskPicRepository interface {
	DeactivateActiveForTask(ctx context.Context, exec db.Executor, taskID string) error
	CreatePhase(ctx context.Context, exec db.Executor, taskID, statusID, userID string, assignedBy *string) error
	ListGroupForStatus(ctx context.Context, exec db.Executor, projectID, statusID string) ([]repository.PicGroupMember, error)
	IsActivePic(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error)
}

// taskDependencyChecker -- reuse TaskDependencyRepository (Phase 3).
// Interface didefinisikan di consumer -- cuma method yang dipakai
// TaskService.SetStatus (HARD-BLOCK + notify successor PIC, S4-48/50).
type taskDependencyChecker interface {
	ListIncompletePredecessors(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskDependency, error)
	NotifySuccessorPics(ctx context.Context, exec db.Executor, taskID string, unblocked bool) error
}

// taskProjectResolver -- reuse ProjectRepository.GetWorkspaceID +
// GetAllowEditorStoryPoints (Phase 4, S4-56).
type taskProjectResolver interface {
	GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error)
	GetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string) (bool, error)
}

// taskCustomStatuses -- reuse CustomStatusRepository.
type taskCustomStatuses interface {
	GetBacklogStatus(ctx context.Context, exec db.Executor, workspaceID string) (*repository.CustomStatus, error)
	Get(ctx context.Context, exec db.Executor, statusID string) (*repository.CustomStatus, error)
}

// taskStatusSessionRepository -- reuse TaskStatusSessionRepository (Phase
// 4, US-018b/018c). Interface didefinisikan di consumer -- cuma method
// yang dipakai TaskService.
type taskStatusSessionRepository interface {
	OpenSession(ctx context.Context, exec db.Executor, taskID, statusID string, isRegression bool, triggeredBy string) error
	CloseActiveSession(ctx context.Context, exec db.Executor, taskID string) error
	StartWork(ctx context.Context, exec db.Executor, taskID string) error
	ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskStatusSession, error)
	NotifyRegression(ctx context.Context, exec db.Executor, taskID, projectID string) error
}

type TaskService struct {
	repo         taskRepository
	pics         taskPicRepository
	deps         taskDependencyChecker
	sessions     taskStatusSessionRepository
	projects     taskProjectResolver
	statuses     taskCustomStatuses
	rbac         sprintWorkspaceRoleChecker
	projectRoles sprintProjectRoleChecker
}

func NewTaskService(repo taskRepository, pics taskPicRepository, deps taskDependencyChecker, sessions taskStatusSessionRepository, projects taskProjectResolver, statuses taskCustomStatuses, rbac sprintWorkspaceRoleChecker, projectRoles sprintProjectRoleChecker) *TaskService {
	return &TaskService{repo: repo, pics: pics, deps: deps, sessions: sessions, projects: projects, statuses: statuses, rbac: rbac, projectRoles: projectRoles}
}

// authorize -- identik SprintService.authorize (viewer/division_viewer
// ditolak) -- duplikasi kecil disengaja, pola sama ProjectService/
// ProjectMemberService yang masing-masing punya authorize sendiri, bukan
// satu helper lintas-service untuk satu pengecekan sederhana.
func (s *TaskService) authorize(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	_, err := s.resolveRole(ctx, exec, projectID, actorID, actorRole)
	return err
}

// resolveRole -- role project_scoped_role/workspace_role aktor di project
// ini ("" untuk PA/GA -- Full mode selalu, tidak perlu role spesifik).
// Dipakai authorize() DAN SetStatus (Phase 2: PIC Handoff Bebas vs Terbatas
// -- glossary "Editor/Approver TIDAK bisa pilih PIC bebas").
func (s *TaskService) resolveRole(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) (string, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return "", nil
	}
	if role, found, err := s.projectRoles.GetRole(ctx, exec, projectID, actorID); err == nil && found {
		if role == "viewer" {
			return "", fmt.Errorf("service.resolveRole: %w", domain.ErrForbidden)
		}
		return role, nil
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return "", fmt.Errorf("service.resolveRole: %w", err)
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return "", fmt.Errorf("service.resolveRole: %w", err)
	}
	if role == "viewer" || role == "division_viewer" {
		return "", fmt.Errorf("service.resolveRole: %w", domain.ErrForbidden)
	}
	return role, nil
}

func validatePriority(priority string) (string, error) {
	priority = strings.ToLower(strings.TrimSpace(priority))
	if priority == "" {
		priority = "medium"
	}
	if !validTaskPriority[priority] {
		return "", fmt.Errorf("service.validatePriority: %w", domain.ErrInvalidInput)
	}
	return priority, nil
}

func validateStoryPoints(sp *int) error {
	if sp != nil && !validFibonacciSP[*sp] {
		return fmt.Errorf("service.validateStoryPoints: %w", domain.ErrInvalidInput)
	}
	return nil
}

// canSetStoryPoints -- Phase 4 (US-018a/S4-56): "PM mengisi; Editor bisa
// diizinkan via project setting". AW/GA/PA selalu boleh (super-role di atas
// PM dalam hierarki workspace/org); Editor cuma boleh kalau project
// mengizinkan (allow_editor_story_points); role lain (Approver/viewer)
// tidak pernah boleh.
func canSetStoryPoints(role string, allowEditorSP bool) bool {
	if role == "" || role == "admin_workspace" || role == "project_manager" {
		return true
	}
	return role == "editor" && allowEditorSP
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Create -- desain "PM Add Task.dc.html": judul wajib, minimal satu
// assignee, status default BACKLOG (S4-13). Story point Fibonacci-only
// (opsional, NULL = belum diestimasi).
func (s *TaskService) Create(ctx context.Context, exec db.Executor, projectID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, assigneeUserIDs []string, actorID, actorRole string) (*repository.Task, error) {
	title = strings.TrimSpace(title)
	if projectID == "" || len(title) < 3 {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrInvalidInput)
	}
	if len(assigneeUserIDs) == 0 {
		return nil, fmt.Errorf("service.Create: %w", domain.ErrTaskAssigneeRequired)
	}
	priority, err := validatePriority(priority)
	if err != nil {
		return nil, err
	}
	if err := validateStoryPoints(storyPoints); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return nil, err
	}

	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	backlog, err := s.statuses.GetBacklogStatus(ctx, exec, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}

	task, err := s.repo.Create(ctx, exec, projectID, sprintID, backlog.ID, title, description, priority, dueDate, estimatedHours, storyPoints, actorID, assigneeUserIDs)
	if err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}
	return task, nil
}

func (s *TaskService) Get(ctx context.Context, exec db.Executor, taskID string) (*repository.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.Get: %w", domain.ErrInvalidInput)
	}
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.Get: %w", err)
	}
	return task, nil
}

func (s *TaskService) List(ctx context.Context, exec db.Executor, projectID string, f repository.TaskFilter) ([]repository.Task, error) {
	if projectID == "" {
		return nil, fmt.Errorf("service.List: %w", domain.ErrInvalidInput)
	}
	list, err := s.repo.List(ctx, exec, projectID, f)
	if err != nil {
		return nil, fmt.Errorf("service.List: %w", err)
	}
	return list, nil
}

// Update -- Phase 4 (S4-55/56): story_points digerbangi TERPISAH dari field
// lain -- gate cuma aktif kalau NILAI benar-benar berubah dari yang
// tersimpan (FE selalu mengirim story_points current di form edit, bukan
// cuma saat sengaja diubah -- gate literal "field dikirim" akan salah
// menolak edit judul/priority biasa untuk Editor tanpa izin SP).
func (s *TaskService) Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, actorID, actorRole string) error {
	title = strings.TrimSpace(title)
	if taskID == "" || len(title) < 3 {
		return fmt.Errorf("service.Update: %w", domain.ErrInvalidInput)
	}
	priority, err := validatePriority(priority)
	if err != nil {
		return err
	}
	if err := validateStoryPoints(storyPoints); err != nil {
		return err
	}
	current, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	role, err := s.resolveRole(ctx, exec, current.ProjectID, actorID, actorRole)
	if err != nil {
		return err
	}
	if !equalIntPtr(current.StoryPoints, storyPoints) {
		allowEditorSP, err := s.projects.GetAllowEditorStoryPoints(ctx, exec, current.ProjectID)
		if err != nil {
			return fmt.Errorf("service.Update: %w", err)
		}
		if !canSetStoryPoints(role, allowEditorSP) {
			return fmt.Errorf("service.Update: %w", domain.ErrStoryPointsNotAllowed)
		}
	}
	if err := s.repo.Update(ctx, exec, taskID, title, description, priority, dueDate, estimatedHours, storyPoints, sprintID); err != nil {
		return fmt.Errorf("service.Update: %w", err)
	}
	return nil
}

// isFullPicMode -- glossary "Phase PIC Handoff": AW/PM/GA/PA = Bebas
// (pilih siapa saja); Editor/Approver = Terbatas KECUALI PIC Group untuk
// status ini kosong (fallback ke Bebas, §5.34).
func isFullPicMode(role string) bool {
	return role == "" || role == "admin_workspace" || role == "project_manager"
}

// SetStatus -- Phase 2 (S4-31/32): WAJIB pilih PIC baru tiap ganti status
// (AC Bruno: "PUT tanpa pic_ids -> 422 pic_required"). PIC lama (kalau ada)
// dinonaktifkan, PIC baru dibuat fase PENDING (belum acknowledge). Editor/
// Approver dibatasi ke PIC Group status tujuan (fallback Bebas kalau
// kosong) -- PM/AW/GA/PA selalu Bebas. Status ber-mode UNDEFINED tidak
// bisa dipilih (sama aturan desain "PM Add Task.dc.html").
//
// Phase 3 (S4-43/48/50): DUA guard tambahan sebelum status benar-benar
// berubah -- (1) completeness: task BACKLOG dengan completeness=incomplete
// tidak boleh pindah KECUALI ke BLOCKED; (2) dependency HARD-BLOCK: task
// dengan predecessor yang belum DONE tidak boleh pindah ke status apa pun
// KECUALI BACKLOG/BLOCKED. Setelah status berubah, successor LANGSUNG
// task ini diberi tahu kalau task ini baru masuk/keluar DONE (S4-50).
func (s *TaskService) SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, picIDs []string, actorID, actorRole string) error {
	if taskID == "" || statusID == "" {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrInvalidInput)
	}
	if len(picIDs) == 0 {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrPicRequired)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	role, err := s.resolveRole(ctx, exec, projectID, actorID, actorRole)
	if err != nil {
		return err
	}
	status, err := s.statuses.Get(ctx, exec, statusID)
	if err != nil {
		return err
	}
	if status.IsUndefined {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrTaskStatusUndefined)
	}

	current, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if current.StatusName == "BACKLOG" && current.Completeness != nil && *current.Completeness == "incomplete" && status.Name != "BLOCKED" {
		return fmt.Errorf("service.SetStatus: %w", domain.ErrTaskIncomplete)
	}
	if status.Name != "BACKLOG" && status.Name != "BLOCKED" {
		blocking, err := s.deps.ListIncompletePredecessors(ctx, exec, taskID)
		if err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
		if len(blocking) > 0 {
			tasks := make([]domain.BlockingTaskInfo, len(blocking))
			for i := range blocking {
				b := &blocking[i]
				if b.PredecessorCode == nil {
					tasks[i] = domain.BlockingTaskInfo{TaskCode: b.PredecessorTitle, Title: b.PredecessorTitle}
				} else {
					tasks[i] = domain.BlockingTaskInfo{TaskCode: *b.PredecessorCode, Title: b.PredecessorTitle}
				}
			}
			return fmt.Errorf("service.SetStatus: %w", &domain.PredecessorBlockingError{BlockingTasks: tasks})
		}
	}

	if !isFullPicMode(role) {
		group, err := s.pics.ListGroupForStatus(ctx, exec, projectID, statusID)
		if err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
		if len(group) > 0 {
			allowed := make(map[string]bool, len(group))
			for _, m := range group {
				allowed[m.UserID] = true
			}
			for _, picID := range picIDs {
				if !allowed[picID] {
					return fmt.Errorf("service.SetStatus: %w", domain.ErrPicNotInGroup)
				}
			}
		}
	}

	currentStatus, err := s.statuses.Get(ctx, exec, current.StatusID)
	if err != nil {
		return err
	}
	isRegression := status.Position < currentStatus.Position

	if err := s.repo.SetStatus(ctx, exec, taskID, statusID, status.Name == "DONE"); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if err := s.pics.DeactivateActiveForTask(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	for _, picID := range picIDs {
		if err := s.pics.CreatePhase(ctx, exec, taskID, statusID, picID, &actorID); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}

	// Phase 4 (S4-62/68): tutup sesi status lama (auto-fill work_started_at
	// kalau belum diklik, S4-67), buka sesi baru untuk status tujuan, dan
	// notify PM+AW kalau ini regresi (S4-68).
	if err := s.sessions.CloseActiveSession(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if err := s.sessions.OpenSession(ctx, exec, taskID, statusID, isRegression, actorID); err != nil {
		return fmt.Errorf("service.SetStatus: %w", err)
	}
	if isRegression {
		if err := s.sessions.NotifyRegression(ctx, exec, taskID, projectID); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}

	wasDone := current.StatusName == "DONE"
	isDone := status.Name == "DONE"
	if isDone != wasDone {
		if err := s.deps.NotifySuccessorPics(ctx, exec, taskID, isDone); err != nil {
			return fmt.Errorf("service.SetStatus: %w", err)
		}
	}
	return nil
}

// StartWork menangani POST /tasks/:id/start-work (Phase 4, US-018b/S4-63).
func (s *TaskService) StartWork(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) error {
	if taskID == "" {
		return fmt.Errorf("service.StartWork: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.sessions.StartWork(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.StartWork: %w", err)
	}
	return nil
}

// ListStatusSessions menangani GET /tasks/:id/status-sessions (Phase 4,
// FE StatusTimeline S4-66 -- Queue/Active/Lead Time dihitung di klien dari
// raw session rows).
func (s *TaskService) ListStatusSessions(ctx context.Context, exec db.Executor, taskID string) ([]repository.TaskStatusSession, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListStatusSessions: %w", domain.ErrInvalidInput)
	}
	list, err := s.sessions.ListForTask(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("service.ListStatusSessions: %w", err)
	}
	return list, nil
}

// SetCompleteness menangani PUT /tasks/:id/completeness (Phase 3, S4-44) --
// hanya pembuat task ATAU PIC aktif yang boleh mengubah flag ini.
func (s *TaskService) SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness, actorID string) error {
	if taskID == "" || (completeness != "complete" && completeness != "incomplete") {
		return fmt.Errorf("service.SetCompleteness: %w", domain.ErrCompletenessInvalid)
	}
	task, err := s.repo.Get(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if task.CreatedBy != actorID {
		isPic, err := s.pics.IsActivePic(ctx, exec, taskID, actorID)
		if err != nil {
			return fmt.Errorf("service.SetCompleteness: %w", err)
		}
		if !isPic {
			return fmt.Errorf("service.SetCompleteness: %w", domain.ErrForbidden)
		}
	}
	if err := s.repo.SetCompleteness(ctx, exec, taskID, completeness); err != nil {
		return fmt.Errorf("service.SetCompleteness: %w", err)
	}
	return nil
}

func (s *TaskService) Delete(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) error {
	if taskID == "" {
		return fmt.Errorf("service.Delete: %w", domain.ErrInvalidInput)
	}
	projectID, err := s.repo.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, exec, projectID, actorID, actorRole); err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, exec, taskID); err != nil {
		return fmt.Errorf("service.Delete: %w", err)
	}
	return nil
}
