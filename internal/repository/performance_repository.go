// Package repository -- PerformanceRepository (EPIC 12 Reporting &
// Analytics, US-075/076/077/078, desain "Performance Dashboard.dc.html").
// Cakupan Project Manager (satu project miliknya) dan Admin Workspace
// (lintas project dalam satu workspace, projectID kosong) -- level Group
// Admin sudah ada terpisah (GroupPerformanceRepository, US-079).
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// PerfTask -- satu baris task untuk Completion Rate/On-Time Rate/Backlog
// Health/Overdue. Rentang waktu difilter dari created_at task.
type PerfTask struct {
	TaskID       string
	ProjectID    string
	ProjectName  string
	TaskCode     *string
	Title        string
	Priority     string
	StatusName   string
	CreatedAt    time.Time
	DueDate      *time.Time
	CompletedAt  *time.Time
	Completeness *string
}

// PerfSession -- satu sesi status untuk Cycle Time/Bottleneck/Regression
// Rate. Rentang waktu difilter dari entered_at sesi (pola sama
// GroupPerformanceRepository.ListStatusDwell).
type PerfSession struct {
	TaskID        string
	ProjectID     string
	Priority      string
	StatusName    string
	EnteredAt     time.Time
	WorkStartedAt *time.Time
	ExitedAt      *time.Time
	IsRegression  bool
}

// PerfAssignee -- satu baris (task, assignee) untuk Member Performance.
type PerfAssignee struct {
	TaskID      string
	UserID      string
	UserName    string
	Priority    string
	StatusName  string
	CompletedAt *time.Time
}

// PerfPicPhase -- satu fase PIC untuk Acknowledge Rate (per member) dan
// Handoff Delay (per project).
type PerfPicPhase struct {
	ProjectID      string
	ProjectName    string
	UserID         string
	UserName       string
	ActivatedAt    time.Time
	AcknowledgedAt *time.Time
}

type PerformanceRepository struct{}

func NewPerformanceRepository() *PerformanceRepository { return &PerformanceRepository{} }

// perfScopeClause -- filter dasar dipakai keempat query di bawah: satu
// workspace + project opsional (PM = satu project, AW = kosong berarti
// lintas seluruh project workspace) + rentang waktu opsional. Dynamic WHERE
// pola sama GroupPerformanceRepository.scopeClause -- nama beda supaya
// tidak bentrok, package sama.
func perfScopeClause(workspaceID, projectID, timeColumn string, since *time.Time) (where string, args []any) {
	clauses := []string{"p.workspace_id = $1", "t.deleted_at IS NULL"}
	args = []any{workspaceID}
	n := 1
	if projectID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("p.id = $%d", n))
		args = append(args, projectID)
	}
	if since != nil {
		n++
		clauses = append(clauses, fmt.Sprintf("%s >= $%d", timeColumn, n))
		args = append(args, *since)
	}
	return strings.Join(clauses, " AND "), args
}

// ListTasks -- Completion Rate, On-Time Rate, Backlog Health, Overdue Task.
func (r *PerformanceRepository) ListTasks(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]PerfTask, error) {
	where, args := perfScopeClause(workspaceID, projectID, "t.created_at", since)
	rows, err := exec.Query(ctx, `
		SELECT t.id, p.id, p.name, t.task_code, t.title, t.priority, cs.name, t.created_at, t.due_date, t.completed_at, t.completeness
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListTasks: %w", err)
	}
	defer rows.Close()

	list := make([]PerfTask, 0)
	for rows.Next() {
		var t PerfTask
		if err := rows.Scan(&t.TaskID, &t.ProjectID, &t.ProjectName, &t.TaskCode, &t.Title, &t.Priority, &t.StatusName, &t.CreatedAt, &t.DueDate, &t.CompletedAt, &t.Completeness); err != nil {
			return nil, fmt.Errorf("repository.ListTasks: scan: %w", err)
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// ListStatusSessions -- Cycle Time, Bottleneck Detection (total & queue
// time), Regression Rate per status asal. DONE dikecualikan -- tidak ada
// "hunian" yang relevan lagi setelah task selesai (pola sama
// GroupPerformanceRepository.ListStatusDwell).
func (r *PerformanceRepository) ListStatusSessions(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]PerfSession, error) {
	where, args := perfScopeClause(workspaceID, projectID, "tss.entered_at", since)
	where += " AND cs.name != 'DONE'"
	rows, err := exec.Query(ctx, `
		SELECT tss.task_id, p.id, t.priority, cs.name, tss.entered_at, tss.work_started_at, tss.exited_at, tss.is_regression
		FROM task_status_sessions tss
		JOIN tasks t ON t.id = tss.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN custom_statuses cs ON cs.id = tss.status_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListStatusSessions: %w", err)
	}
	defer rows.Close()

	list := make([]PerfSession, 0)
	for rows.Next() {
		var s PerfSession
		if err := rows.Scan(&s.TaskID, &s.ProjectID, &s.Priority, &s.StatusName, &s.EnteredAt, &s.WorkStartedAt, &s.ExitedAt, &s.IsRegression); err != nil {
			return nil, fmt.Errorf("repository.ListStatusSessions: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// FirstWorkStarted -- MIN(work_started_at) sesi berstatus IN PROGRESS per
// task, dipakai Cycle Time total (US-076 AC "work_started_at (In Progress
// pertama)") dan Flow Efficiency (Lead/Cycle Time). Key map: taskID.
func (r *PerformanceRepository) FirstWorkStarted(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) (map[string]time.Time, error) {
	where, args := perfScopeClause(workspaceID, projectID, "t.created_at", since)
	rows, err := exec.Query(ctx, `
		SELECT tss.task_id, MIN(tss.work_started_at)
		FROM task_status_sessions tss
		JOIN tasks t ON t.id = tss.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN custom_statuses cs ON cs.id = tss.status_id
		WHERE `+where+` AND cs.name = 'IN PROGRESS' AND tss.work_started_at IS NOT NULL
		GROUP BY tss.task_id
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.FirstWorkStarted: %w", err)
	}
	defer rows.Close()

	out := make(map[string]time.Time)
	for rows.Next() {
		var taskID string
		var t time.Time
		if err := rows.Scan(&taskID, &t); err != nil {
			return nil, fmt.Errorf("repository.FirstWorkStarted: scan: %w", err)
		}
		out[taskID] = t
	}
	return out, rows.Err()
}

// ListAssignees -- Task Load, Completion Rate, Avg Completion Time per
// member (Member Performance). Task tanpa assignee tidak muncul (task load
// nol tidak relevan ditampilkan per member yang tidak ada).
func (r *PerformanceRepository) ListAssignees(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]PerfAssignee, error) {
	where, args := perfScopeClause(workspaceID, projectID, "t.created_at", since)
	rows, err := exec.Query(ctx, `
		SELECT t.id, u.id, u.display_name, t.priority, cs.name, t.completed_at
		FROM task_assignees ta
		JOIN tasks t ON t.id = ta.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN custom_statuses cs ON cs.id = t.status_id
		JOIN users u ON u.id = ta.user_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListAssignees: %w", err)
	}
	defer rows.Close()

	list := make([]PerfAssignee, 0)
	for rows.Next() {
		var a PerfAssignee
		if err := rows.Scan(&a.TaskID, &a.UserID, &a.UserName, &a.Priority, &a.StatusName, &a.CompletedAt); err != nil {
			return nil, fmt.Errorf("repository.ListAssignees: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListPicPhases -- Acknowledge Rate (per member) dan Handoff Delay (per
// project). Rentang waktu difilter dari activated_at (kapan handoff
// dikirim), bukan created_at task.
func (r *PerformanceRepository) ListPicPhases(ctx context.Context, exec db.Executor, workspaceID, projectID string, since *time.Time) ([]PerfPicPhase, error) {
	where, args := perfScopeClause(workspaceID, projectID, "tpp.activated_at", since)
	rows, err := exec.Query(ctx, `
		SELECT p.id, p.name, u.id, u.display_name, tpp.activated_at, tpp.acknowledged_at
		FROM task_pic_phases tpp
		JOIN tasks t ON t.id = tpp.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN users u ON u.id = tpp.user_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPicPhases: %w", err)
	}
	defer rows.Close()

	list := make([]PerfPicPhase, 0)
	for rows.Next() {
		var h PerfPicPhase
		if err := rows.Scan(&h.ProjectID, &h.ProjectName, &h.UserID, &h.UserName, &h.ActivatedAt, &h.AcknowledgedAt); err != nil {
			return nil, fmt.Errorf("repository.ListPicPhases: scan: %w", err)
		}
		list = append(list, h)
	}
	return list, rows.Err()
}
