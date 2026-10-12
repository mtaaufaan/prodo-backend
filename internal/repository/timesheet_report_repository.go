// Package repository -- TimesheetReportRepository (menu "Timesheet & Analitik"
// PM, "PM Timesheet.dc.html", IG-128). Agregat jam tercatat per PROJECT di
// atas tabel time_entries (IG-97). Jam tercatat = HANYA entri is_approved =
// TRUE (timer otomatis disetujui + entri manual yang sudah di-approve), sama
// aturan SumLoggedMinutesForTask, supaya angka tidak bisa dimanipulasi lewat
// entri manual yang belum diverifikasi. Otorisasi digerbangi di service; RLS
// time_entries tetap berlaku (PM/AW workspace melihat entri semua member).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// TimesheetStatus -- satu status project (urut position) untuk batang
// "JAM TERCATAT PER STATUS".
type TimesheetStatus struct {
	Name        string
	ColorToken  string
	IsUndefined bool
}

// TimesheetTask -- satu task project + jam tercatat (menit) dan PIC aktif.
type TimesheetTask struct {
	TaskID         string
	Code           string
	Title          string
	StatusName     string
	EstimatedHours float64
	LoggedMinutes  int
	ActivePICs     []string
}

// TimesheetMember -- satu baris tabel Per-Member.
type TimesheetMember struct {
	UserID        string
	Name          string
	Role          string
	LoggedMinutes int
	TaskCount     int // task berbeda yang dicatat jamnya
	ActiveCount   int // task belum selesai yang dipegang sebagai PIC aktif
}

// TimesheetPendingEntry -- entri manual + konteks task untuk antrian
// persetujuan lintas task.
type TimesheetPendingEntry struct {
	TimeEntry
	TaskCode  string
	TaskTitle string
}

// MyTimesheetEntry -- entri milik sendiri + konteks task/project.
type MyTimesheetEntry struct {
	TimeEntry
	TaskCode    string
	TaskTitle   string
	ProjectID   string
	ProjectName string
}

type TimesheetReportRepository struct{}

func NewTimesheetReportRepository() *TimesheetReportRepository {
	return &TimesheetReportRepository{}
}

// ListStatuses -- status project berurut position.
func (r *TimesheetReportRepository) ListStatuses(ctx context.Context, exec db.Executor, projectID string) ([]TimesheetStatus, error) {
	rows, err := exec.Query(ctx, `
		SELECT name, COALESCE(color_token, ''), is_undefined
		FROM custom_statuses
		WHERE scope_type = 'project' AND scope_id = $1
		ORDER BY position, created_at
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.TimesheetListStatuses: %w", err)
	}
	defer rows.Close()
	list := make([]TimesheetStatus, 0)
	for rows.Next() {
		var s TimesheetStatus
		if err := rows.Scan(&s.Name, &s.ColorToken, &s.IsUndefined); err != nil {
			return nil, fmt.Errorf("repository.TimesheetListStatuses: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// ListTasks -- seluruh task aktif (tidak dihapus) project + jam tercatat +
// PIC aktif (nama, urut abjad). Task CANCELED ikut (jam yang sudah terpakai
// tetap nyata), BACKLOG/DONE juga.
func (r *TimesheetReportRepository) ListTasks(ctx context.Context, exec db.Executor, projectID string) ([]TimesheetTask, error) {
	rows, err := exec.Query(ctx, `
		SELECT t.id, t.task_code, t.title, cs.name, COALESCE(t.estimated_hours, 0)::float8,
		       COALESCE((SELECT SUM(te.duration_minutes) FROM time_entries te
		                 WHERE te.task_id = t.id AND te.is_approved = TRUE), 0)::int,
		       COALESCE((SELECT array_agg(u.display_name ORDER BY u.display_name)
		                 FROM task_pic_phases tpp JOIN users u ON u.id = tpp.user_id
		                 WHERE tpp.task_id = t.id AND tpp.is_active), ARRAY[]::text[])
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL
		ORDER BY t.created_at, t.task_code
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.TimesheetListTasks: %w", err)
	}
	defer rows.Close()
	list := make([]TimesheetTask, 0)
	for rows.Next() {
		var t TimesheetTask
		if err := rows.Scan(&t.TaskID, &t.Code, &t.Title, &t.StatusName, &t.EstimatedHours, &t.LoggedMinutes, &t.ActivePICs); err != nil {
			return nil, fmt.Errorf("repository.TimesheetListTasks: scan: %w", err)
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// ListMembers -- member yang pernah mencatat jam (approved) ATAU sedang jadi
// PIC aktif pada task belum selesai project ini. Role = role workspace, atau
// role project-scoped, atau 'project_manager' untuk PM project; kosong bila
// tidak ketemu.
func (r *TimesheetReportRepository) ListMembers(ctx context.Context, exec db.Executor, projectID string) ([]TimesheetMember, error) {
	rows, err := exec.Query(ctx, `
		WITH logged AS (
			SELECT te.user_id, COUNT(DISTINCT te.task_id) AS task_count, SUM(te.duration_minutes)::int AS minutes
			FROM time_entries te JOIN tasks t ON t.id = te.task_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND te.is_approved = TRUE
			GROUP BY te.user_id
		), active AS (
			SELECT tpp.user_id, COUNT(DISTINCT tpp.task_id) AS active_count
			FROM task_pic_phases tpp
			JOIN tasks t ON t.id = tpp.task_id
			JOIN custom_statuses cs ON cs.id = t.status_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND tpp.is_active
			  AND cs.name NOT IN ('DONE', 'CANCELED')
			GROUP BY tpp.user_id
		)
		SELECT u.id, u.display_name,
		       COALESCE(wm.role::text,
		                (SELECT pm.role::text FROM project_members pm WHERE pm.project_id = $1 AND pm.user_id = u.id),
		                CASE WHEN EXISTS (SELECT 1 FROM project_managers pg WHERE pg.project_id = $1 AND pg.user_id = u.id)
		                     THEN 'project_manager' END, ''),
		       COALESCE(l.minutes, 0), COALESCE(l.task_count, 0), COALESCE(a.active_count, 0)
		FROM users u
		LEFT JOIN logged l ON l.user_id = u.id
		LEFT JOIN active a ON a.user_id = u.id
		LEFT JOIN projects p ON p.id = $1
		LEFT JOIN workspace_members wm ON wm.workspace_id = p.workspace_id AND wm.user_id = u.id
		WHERE l.user_id IS NOT NULL OR a.user_id IS NOT NULL
		ORDER BY COALESCE(l.minutes, 0) DESC, u.display_name
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.TimesheetListMembers: %w", err)
	}
	defer rows.Close()
	list := make([]TimesheetMember, 0)
	for rows.Next() {
		var m TimesheetMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Role, &m.LoggedMinutes, &m.TaskCount, &m.ActiveCount); err != nil {
			return nil, fmt.Errorf("repository.TimesheetListMembers: scan: %w", err)
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// PendingCount -- jumlah entri manual menunggu persetujuan (badge tab).
func (r *TimesheetReportRepository) PendingCount(ctx context.Context, exec db.Executor, projectID string) (int, error) {
	var n int
	err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FROM time_entries te JOIN tasks t ON t.id = te.task_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND te.entry_type = 'manual' AND te.is_approved IS NULL
	`, projectID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository.TimesheetPendingCount: %w", err)
	}
	return n, nil
}

// ListManualEntries -- antrian persetujuan: entri MANUAL project, difilter
// approvalStatus (pending, approved, rejected, atau kosong = semua), terbaru dulu.
func (r *TimesheetReportRepository) ListManualEntries(ctx context.Context, exec db.Executor, projectID, approvalStatus string) ([]TimesheetPendingEntry, error) {
	query := `
		SELECT te.id, te.task_id, te.user_id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		       te.started_at, te.ended_at, te.duration_minutes, te.entry_type, te.note, te.is_approved, te.rejection_note,
		       te.created_at, te.updated_at, t.task_code, t.title
		FROM time_entries te
		JOIN tasks t ON t.id = te.task_id
		JOIN users u ON u.id = te.user_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND te.entry_type = 'manual'`
	switch approvalStatus {
	case "pending":
		query += " AND te.is_approved IS NULL"
	case "approved":
		query += " AND te.is_approved = TRUE"
	case "rejected":
		query += " AND te.is_approved = FALSE"
	}
	query += " ORDER BY te.created_at DESC"
	rows, err := exec.Query(ctx, query, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.TimesheetListManualEntries: %w", err)
	}
	defer rows.Close()
	list := make([]TimesheetPendingEntry, 0)
	for rows.Next() {
		var e TimesheetPendingEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.UserID, &e.UserName, &e.UserEmail, &e.StartedAt, &e.EndedAt, &e.DurationMinutes,
			&e.EntryType, &e.Note, &e.IsApproved, &e.RejectionNote, &e.CreatedAt, &e.UpdatedAt, &e.TaskCode, &e.TaskTitle); err != nil {
			return nil, fmt.Errorf("repository.TimesheetListManualEntries: scan: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// ListMine -- timesheet pribadi: SEMUA entri milik user (timer + manual, semua
// status approval) pada rentang [from, to] (tanggal mulai), terbaru dulu.
// from/to nil = tanpa batas.
func (r *TimesheetReportRepository) ListMine(ctx context.Context, exec db.Executor, userID string, from, to *time.Time) ([]MyTimesheetEntry, error) {
	query := `
		SELECT te.id, te.task_id, te.user_id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		       te.started_at, te.ended_at, te.duration_minutes, te.entry_type, te.note, te.is_approved, te.rejection_note,
		       te.created_at, te.updated_at, t.task_code, t.title, p.id, p.name
		FROM time_entries te
		JOIN tasks t ON t.id = te.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN users u ON u.id = te.user_id
		WHERE te.user_id = $1 AND t.deleted_at IS NULL`
	args := []any{userID}
	if from != nil {
		args = append(args, *from)
		query += fmt.Sprintf(" AND te.started_at >= $%d", len(args))
	}
	if to != nil {
		args = append(args, *to)
		query += fmt.Sprintf(" AND te.started_at < $%d", len(args))
	}
	query += " ORDER BY te.started_at DESC"
	rows, err := exec.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.TimesheetListMine: %w", err)
	}
	defer rows.Close()
	list := make([]MyTimesheetEntry, 0)
	for rows.Next() {
		var e MyTimesheetEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.UserID, &e.UserName, &e.UserEmail, &e.StartedAt, &e.EndedAt, &e.DurationMinutes,
			&e.EntryType, &e.Note, &e.IsApproved, &e.RejectionNote, &e.CreatedAt, &e.UpdatedAt,
			&e.TaskCode, &e.TaskTitle, &e.ProjectID, &e.ProjectName); err != nil {
			return nil, fmt.Errorf("repository.TimesheetListMine: scan: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// LogExport -- audit trail ekspor timesheet (aksi ACCESS di desain): satu
// baris audit_logs entity project, snapshot jumlah baris di state_after.
// Nama action berakhiran .report_downloaded supaya otomatis diklasifikasi
// ACCESS oleh actionTypeCase (audit trail workspace/grup).
func (r *TimesheetReportRepository) LogExport(ctx context.Context, exec db.Executor, actorID, actorRole, projectID, workspaceID, projectName string, rows, loggedMinutes int) error {
	return insertProjectAudit(ctx, exec, actorID, actorRole, "timesheet.report_downloaded", projectID, workspaceID, nil,
		map[string]any{"rows": rows, "logged_minutes": loggedMinutes, "format": "csv"},
		map[string]any{"name": projectName})
}

// ProjectInfo -- workspace + nama project (gerbang otorisasi, nama berkas CSV).
// ErrProjectNotFound bila project tidak ada / tidak terlihat (RLS).
func (r *TimesheetReportRepository) ProjectInfo(ctx context.Context, exec db.Executor, projectID string) (workspaceID, name string, err error) {
	err = exec.QueryRow(ctx, `SELECT workspace_id, name FROM projects WHERE id = $1`, projectID).Scan(&workspaceID, &name)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", "", fmt.Errorf("repository.TimesheetProjectInfo: %w", domain.ErrProjectNotFound)
		}
		return "", "", fmt.Errorf("repository.TimesheetProjectInfo: %w", err)
	}
	return workspaceID, name, nil
}
