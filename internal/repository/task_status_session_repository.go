// Package repository -- TaskStatusSessionRepository (Task Management Core
// Phase 4, US-018b/018c: Status Time Tracking + Regression). Satu row per
// sesi status task (DATABASE_SCHEMA.md §5.37) -- TIDAK PERNAH diupdate
// selain exited_at/work_started_at saat sesi ditutup; riwayat lengkap
// tersimpan (pola sama task_pic_phases Phase 2).
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type TaskStatusSession struct {
	ID            string
	TaskID        string
	StatusID      string
	StatusName    string
	SessionNo     int
	EnteredAt     time.Time
	WorkStartedAt *time.Time
	IsAutoStart   bool
	ExitedAt      *time.Time
	IsRegression  bool
	TriggeredBy   *string
}

type TaskStatusSessionRepository struct{}

func NewTaskStatusSessionRepository() *TaskStatusSessionRepository {
	return &TaskStatusSessionRepository{}
}

// OpenSession -- S4-62: buat sesi baru untuk status ini. session_no
// bertambah tiap kali task KEMBALI ke status yang sama (regresi/reguler).
func (r *TaskStatusSessionRepository) OpenSession(ctx context.Context, exec db.Executor, taskID, statusID string, isRegression bool, triggeredBy string) error {
	var nextNo int
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(MAX(session_no), 0) + 1 FROM task_status_sessions WHERE task_id = $1 AND status_id = $2
	`, taskID, statusID).Scan(&nextNo); err != nil {
		return fmt.Errorf("repository.OpenSession: hitung session_no: %w", err)
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO task_status_sessions (task_id, status_id, session_no, is_regression, triggered_by)
		VALUES ($1, $2, $3, $4, $5)
	`, taskID, statusID, nextNo, isRegression, triggeredBy)
	if err != nil {
		return fmt.Errorf("repository.OpenSession: %w", err)
	}
	return nil
}

// CloseActiveSession -- dipanggil SEBELUM OpenSession saat status berganti
// (S4-62). Auto-fill work_started_at=entered_at kalau belum diklik (S4-67,
// ditandai is_auto_start=TRUE supaya FE bisa render ikon "⏱ auto" tanpa
// heuristik tebak-tebak perbandingan timestamp).
func (r *TaskStatusSessionRepository) CloseActiveSession(ctx context.Context, exec db.Executor, taskID string) error {
	_, err := exec.Exec(ctx, `
		UPDATE task_status_sessions
		SET exited_at = NOW(),
		    is_auto_start = (work_started_at IS NULL),
		    work_started_at = COALESCE(work_started_at, entered_at)
		WHERE task_id = $1 AND exited_at IS NULL
	`, taskID)
	if err != nil {
		return fmt.Errorf("repository.CloseActiveSession: %w", err)
	}
	return nil
}

// StartWork -- S4-63: PIC/assignee klik "Mulai Pengerjaan". 404 kalau task
// tidak punya sesi aktif sama sekali, 409 kalau sesi aktif sudah start.
func (r *TaskStatusSessionRepository) StartWork(ctx context.Context, exec db.Executor, taskID string) error {
	var workStartedAt *time.Time
	err := exec.QueryRow(ctx, `
		SELECT work_started_at FROM task_status_sessions WHERE task_id = $1 AND exited_at IS NULL
	`, taskID).Scan(&workStartedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.StartWork: %w", domain.ErrNoActiveStatusSession)
		}
		return fmt.Errorf("repository.StartWork: %w", err)
	}
	if workStartedAt != nil {
		return fmt.Errorf("repository.StartWork: %w", domain.ErrWorkAlreadyStarted)
	}
	if _, err := exec.Exec(ctx, `
		UPDATE task_status_sessions SET work_started_at = NOW() WHERE task_id = $1 AND exited_at IS NULL
	`, taskID); err != nil {
		return fmt.Errorf("repository.StartWork: %w", err)
	}
	return nil
}

// ListForTask -- riwayat sesi lengkap (FE StatusTimeline, S4-66), terlama
// dulu supaya Queue/Active/Lead Time gampang diakumulasi berurutan.
func (r *TaskStatusSessionRepository) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]TaskStatusSession, error) {
	rows, err := exec.Query(ctx, `
		SELECT tss.id, tss.task_id, tss.status_id, cs.name, tss.session_no, tss.entered_at,
		       tss.work_started_at, tss.is_auto_start, tss.exited_at, tss.is_regression, tss.triggered_by
		FROM task_status_sessions tss
		JOIN custom_statuses cs ON cs.id = tss.status_id
		WHERE tss.task_id = $1
		ORDER BY tss.entered_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TaskStatusSession, 0)
	for rows.Next() {
		var s TaskStatusSession
		if err := rows.Scan(&s.ID, &s.TaskID, &s.StatusID, &s.StatusName, &s.SessionNo, &s.EnteredAt,
			&s.WorkStartedAt, &s.IsAutoStart, &s.ExitedAt, &s.IsRegression, &s.TriggeredBy); err != nil {
			return nil, fmt.Errorf("repository.ListForTask: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// NotifyRegression -- S4-68: notify PM project + seluruh Admin Workspace
// begitu regresi terjadi. Satu INSERT...SELECT (PM dari projects.pm_user_id,
// AW dari workspace_members role admin_workspace), bukan loop per-recipient.
func (r *TaskStatusSessionRepository) NotifyRegression(ctx context.Context, exec db.Executor, taskID, projectID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
		SELECT DISTINCT recipients.user_id, NULL::uuid, 'task_regressed', 'task', $1::uuid,
		       'Task Mengalami Regresi', 'Sebuah task kembali ke status yang lebih awal dari sebelumnya.'
		FROM (
			SELECT pm_user_id AS user_id FROM projects WHERE id = $2 AND pm_user_id IS NOT NULL
			UNION
			SELECT wm.user_id FROM workspace_members wm
			JOIN projects p ON p.workspace_id = wm.workspace_id
			WHERE p.id = $2 AND wm.role = 'admin_workspace'
		) recipients
	`, taskID, projectID)
	if err != nil {
		return fmt.Errorf("repository.NotifyRegression: %w", err)
	}
	return nil
}
