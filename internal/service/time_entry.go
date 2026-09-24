// Package service -- TimeEntryService (Timesheet, dimajukan dari Sprint S8
// asli ke Track S5/IG-97, US-036/037, API_CONTRACT.md §15). Start/stop
// timer (satu timer per user, PK active_timers=user_id), entri manual +
// approval workflow (AW/PM only). Chip header "PM Task Detail.dc.html"
// (LOGGED/ESTIMASI JAM) dan field RINGKASAN "JAM TERCATAT" dihitung dari
// SumLoggedMinutesForTask (HANYA entri approved).
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// timeEntryRepository -- interface didefinisikan di consumer, §3.9.
type timeEntryRepository interface {
	StartTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*repository.ActiveTimer, error)
	GetActiveTimer(ctx context.Context, exec db.Executor, userID string) (*repository.ActiveTimer, error)
	StopTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*repository.TimeEntry, error)
	CreateManual(ctx context.Context, exec db.Executor, taskID, userID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error)
	HasOverlap(ctx context.Context, exec db.Executor, userID string, startedAt, endedAt time.Time, excludeID string) (bool, error)
	Get(ctx context.Context, exec db.Executor, id string) (*repository.TimeEntry, error)
	UpdateManual(ctx context.Context, exec db.Executor, id string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error)
	Approve(ctx context.Context, exec db.Executor, id, approvedBy string) error
	Reject(ctx context.Context, exec db.Executor, id, rejectedBy, note string) error
	ListForTask(ctx context.Context, exec db.Executor, taskID, approvalStatus, userID string) ([]repository.TimeEntry, error)
	SumLoggedMinutesForTask(ctx context.Context, exec db.Executor, taskID string) (int, error)
}

// timeEntryTaskResolver -- reuse TaskRepository.GetProjectID.
type timeEntryTaskResolver interface {
	GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error)
}

type TimeEntryService struct {
	repo     timeEntryRepository
	tasks    timeEntryTaskResolver
	projects taskProjectResolver
	rbac     sprintWorkspaceRoleChecker
}

func NewTimeEntryService(repo timeEntryRepository, tasks timeEntryTaskResolver, projects taskProjectResolver, rbac sprintWorkspaceRoleChecker) *TimeEntryService {
	return &TimeEntryService{repo: repo, tasks: tasks, projects: projects, rbac: rbac}
}

// isApprover -- AW/PM di workspace project ini, atau PA/GA (bypass penuh)
// -- satu-satunya role yang boleh lihat entri user lain/approve/reject
// (API_CONTRACT.md §15).
func (s *TimeEntryService) isApprover(ctx context.Context, exec db.Executor, taskID, actorID, actorRole string) (bool, error) {
	if actorRole == "platform_admin" || actorRole == "group_admin" {
		return true, nil
	}
	projectID, err := s.tasks.GetProjectID(ctx, exec, taskID)
	if err != nil {
		return false, err
	}
	workspaceID, err := s.projects.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return false, err
	}
	role, err := s.rbac.GetMemberRole(ctx, exec, workspaceID, actorID)
	if err != nil {
		return false, err
	}
	return role == "admin_workspace" || role == "project_manager", nil
}

// StartTimer menangani POST /tasks/:id/time-entries/start.
func (s *TimeEntryService) StartTimer(ctx context.Context, exec db.Executor, taskID, actorID string) (*repository.ActiveTimer, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.StartTimer: %w", domain.ErrInvalidInput)
	}
	timer, err := s.repo.StartTimer(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.StartTimer: %w", err)
	}
	return timer, nil
}

// GetActive menangani GET /tasks/:id/time-entries/active -- nil (bukan
// error) kalau user tidak punya timer aktif SAMA SEKALI, atau timer
// aktifnya ternyata untuk task lain (API_CONTRACT.md: "Cek apakah user
// yang sedang login memiliki timer aktif UNTUK TASK INI").
func (s *TimeEntryService) GetActive(ctx context.Context, exec db.Executor, taskID, actorID string) (*repository.ActiveTimer, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.GetActive: %w", domain.ErrInvalidInput)
	}
	timer, err := s.repo.GetActiveTimer(ctx, exec, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.GetActive: %w", err)
	}
	if timer == nil || timer.TaskID != taskID {
		return nil, nil
	}
	return timer, nil
}

// StopTimer menangani POST /tasks/:id/time-entries/stop.
func (s *TimeEntryService) StopTimer(ctx context.Context, exec db.Executor, taskID, actorID string) (*repository.TimeEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.StopTimer: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.StopTimer(ctx, exec, taskID, actorID)
	if err != nil {
		return nil, fmt.Errorf("service.StopTimer: %w", err)
	}
	return entry, nil
}

// CreateManual menangani POST /tasks/:id/time-entries.
func (s *TimeEntryService) CreateManual(ctx context.Context, exec db.Executor, taskID, actorID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error) {
	if taskID == "" || !endedAt.After(startedAt) {
		return nil, fmt.Errorf("service.CreateManual: %w", domain.ErrInvalidInput)
	}
	overlap, err := s.repo.HasOverlap(ctx, exec, actorID, startedAt, endedAt, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	if overlap {
		return nil, fmt.Errorf("service.CreateManual: %w", domain.ErrTimeEntryOverlap)
	}
	entry, err := s.repo.CreateManual(ctx, exec, taskID, actorID, startedAt, endedAt, note)
	if err != nil {
		return nil, fmt.Errorf("service.CreateManual: %w", err)
	}
	return entry, nil
}

// UpdateManual menangani PATCH /time-entries/:id -- hanya pemilik entri,
// hanya saat masih pending (repo.UpdateManual sudah menggerbangi
// is_approved IS NULL, di sini cukup cek kepemilikan).
func (s *TimeEntryService) UpdateManual(ctx context.Context, exec db.Executor, entryID, actorID string, startedAt, endedAt time.Time, note *string) (*repository.TimeEntry, error) {
	if entryID == "" || !endedAt.After(startedAt) {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrInvalidInput)
	}
	current, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	if current.UserID != actorID {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrForbidden)
	}
	overlap, err := s.repo.HasOverlap(ctx, exec, actorID, startedAt, endedAt, entryID)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	if overlap {
		return nil, fmt.Errorf("service.UpdateManual: %w", domain.ErrTimeEntryOverlap)
	}
	entry, err := s.repo.UpdateManual(ctx, exec, entryID, startedAt, endedAt, note)
	if err != nil {
		return nil, fmt.Errorf("service.UpdateManual: %w", err)
	}
	return entry, nil
}

// Approve menangani POST /time-entries/:id/approve -- AW/PM saja.
func (s *TimeEntryService) Approve(ctx context.Context, exec db.Executor, entryID, actorID, actorRole string) error {
	if entryID == "" {
		return fmt.Errorf("service.Approve: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	ok, err := s.isApprover(ctx, exec, entry.TaskID, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.Approve: %w", domain.ErrForbidden)
	}
	if err := s.repo.Approve(ctx, exec, entryID, actorID); err != nil {
		return fmt.Errorf("service.Approve: %w", err)
	}
	return nil
}

// Reject menangani POST /time-entries/:id/reject -- AW/PM saja, wajib
// rejection_note (US-037 AC, sama CHECK constraint DB ck_rejection_note).
func (s *TimeEntryService) Reject(ctx context.Context, exec db.Executor, entryID, actorID, actorRole, note string) error {
	if entryID == "" || note == "" {
		return fmt.Errorf("service.Reject: %w", domain.ErrInvalidInput)
	}
	entry, err := s.repo.Get(ctx, exec, entryID)
	if err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	ok, err := s.isApprover(ctx, exec, entry.TaskID, actorID, actorRole)
	if err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	if !ok {
		return fmt.Errorf("service.Reject: %w", domain.ErrForbidden)
	}
	if err := s.repo.Reject(ctx, exec, entryID, actorID, note); err != nil {
		return fmt.Errorf("service.Reject: %w", err)
	}
	return nil
}

// ListForTask menangani GET /tasks/:id/time-entries -- AW/PM lihat semua
// (opsional filter userID), user lain SELALU dipaksa filter ke diri
// sendiri (API_CONTRACT.md: "user lain hanya milik sendiri") apa pun
// filterUserID yang diminta.
func (s *TimeEntryService) ListForTask(ctx context.Context, exec db.Executor, taskID, approvalStatus, filterUserID, actorID, actorRole string) ([]repository.TimeEntry, error) {
	if taskID == "" {
		return nil, fmt.Errorf("service.ListForTask: %w", domain.ErrInvalidInput)
	}
	approver, err := s.isApprover(ctx, exec, taskID, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	if !approver {
		filterUserID = actorID
	}
	list, err := s.repo.ListForTask(ctx, exec, taskID, approvalStatus, filterUserID)
	if err != nil {
		return nil, fmt.Errorf("service.ListForTask: %w", err)
	}
	return list, nil
}

// SumLoggedMinutes -- dipakai TaskHandler (chip header + field RINGKASAN
// "JAM TERCATAT"), bukan endpoint HTTP sendiri.
func (s *TimeEntryService) SumLoggedMinutes(ctx context.Context, exec db.Executor, taskID string) (int, error) {
	return s.repo.SumLoggedMinutesForTask(ctx, exec, taskID)
}
