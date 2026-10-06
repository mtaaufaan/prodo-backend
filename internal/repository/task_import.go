package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// TaskImportSession -- satu sesi status historis (IG-120 tahap c). WorkStarted:
// work_started_at diisi = EnteredAt dan is_auto_start = TRUE (status kerja
// IN PROGRESS/UNDER REVIEW, atau sesi yang sudah ditutup -- sama perilaku
// CloseActiveSession).
type TaskImportSession struct {
	StatusID    string
	EnteredAt   time.Time
	ExitedAt    *time.Time
	WorkStarted bool
}

// TaskImportPhase -- satu PIC fase historis/aktif (task_pic_phases). Diterima
// (acknowledged_at = ActivatedAt): data lama dianggap sudah dikonfirmasi.
type TaskImportPhase struct {
	StatusID      string
	UserID        string
	ActivatedAt   time.Time
	DeactivatedAt *time.Time
	Active        bool
}

// TaskImportInput -- satu task dari import CSV (IG-120 tahap b/c). Status awal
// BEBAS (kolom status CSV), bukan selalu BACKLOG seperti TaskRepository.Create.
type TaskImportInput struct {
	ProjectID, WorkspaceID, ActorID, ActorRole string
	StatusID, StatusName                       string
	Title, Priority                            string
	SprintID                                   *string
	StartDate, DueDate                         *time.Time
	EstimatedHours                             *float64
	StoryPoints                                *int
	AssigneeUserIDs                            []string
	// Riwayat (tahap c) -- opsional. Sessions kosong = satu sesi di StatusID
	// sejak sekarang. CreatedAt/CompletedAt menimpa NOW().
	CreatedAt   *time.Time
	CompletedAt *time.Time
	Sessions    []TaskImportSession
	Phases      []TaskImportPhase
}

// CreateImported -- jalur KHUSUS import (bukan TaskRepository.Create): data lama
// dimigrasi apa adanya, jadi
//   - status awal sesuai berkas; sesi status dari riwayat tanggal (Sessions)
//     atau satu sesi sejak sekarang;
//   - TIDAK ada PIC fase awal otomatis atas nama pembuat (Create memberi
//     pelaku PIC BACKLOG; di sini pelaku hanya pengimpor) -- PIC hanya dari
//     kolom pic_* CSV (Phases);
//   - completeness: BACKLOG 'incomplete' (sama task manual, PM menandai
//     Lengkap), status lain 'complete' (kalau tidak, kartu menampilkan
//     "BELUM LENGKAP" padahal task sudah berjalan);
//   - DONE -> completed_at = CompletedAt (tanggal done_at) atau NOW();
//   - created_at = CreatedAt (tanggal masuk backlog) atau NOW();
//   - rule task_created TIDAK dijalankan (dilewati sengaja, lihat service).
func (r *TaskRepository) CreateImported(ctx context.Context, exec db.Executor, in *TaskImportInput) (taskID, taskCode string, err error) {
	taskCode, err = r.nextTaskCode(ctx, exec, in.ProjectID)
	if err != nil {
		return "", "", fmt.Errorf("repository.CreateImported: %w", err)
	}
	completeness := "complete"
	if in.StatusName == "BACKLOG" {
		completeness = "incomplete"
	}
	var completedAt any
	switch {
	case in.CompletedAt != nil:
		completedAt = *in.CompletedAt
	case in.StatusName == "DONE":
		completedAt = time.Now()
	}
	err = exec.QueryRow(ctx, `
		INSERT INTO tasks (project_id, sprint_id, status_id, title, priority, completeness, start_date, due_date, estimated_hours, story_points, task_code, created_by, completed_at, created_at, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, COALESCE($14, NOW()),
			COALESCE((SELECT MAX(position) FROM tasks WHERE project_id = $1 AND status_id = $3), 0) + 1)
		RETURNING id
	`, in.ProjectID, in.SprintID, in.StatusID, in.Title, in.Priority, completeness, in.StartDate, in.DueDate, in.EstimatedHours, in.StoryPoints,
		taskCode, in.ActorID, completedAt, in.CreatedAt).Scan(&taskID)
	if err != nil {
		return "", "", fmt.Errorf("repository.CreateImported: %w", err)
	}
	for _, userID := range in.AssigneeUserIDs {
		if _, err := exec.Exec(ctx, `
			INSERT INTO task_assignees (task_id, user_id, assignee_role, assigned_by)
			VALUES ($1, $2, 'contributor', $3)
		`, taskID, userID, in.ActorID); err != nil {
			return "", "", fmt.Errorf("repository.CreateImported: assignee: %w", err)
		}
	}

	if len(in.Sessions) == 0 {
		if _, err := exec.Exec(ctx, `
			INSERT INTO task_status_sessions (task_id, status_id, session_no, triggered_by)
			VALUES ($1, $2, 1, $3)
		`, taskID, in.StatusID, in.ActorID); err != nil {
			return "", "", fmt.Errorf("repository.CreateImported: sesi status awal: %w", err)
		}
	}
	for i := range in.Sessions {
		ss := &in.Sessions[i]
		var workStarted any
		if ss.WorkStarted {
			workStarted = ss.EnteredAt
		}
		// Tiap status paling banyak SATU sesi per baris CSV, jadi session_no = 1.
		if _, err := exec.Exec(ctx, `
			INSERT INTO task_status_sessions (task_id, status_id, session_no, entered_at, exited_at, work_started_at, is_auto_start, triggered_by)
			VALUES ($1, $2, 1, $3, $4, $5, $6, $7)
		`, taskID, ss.StatusID, ss.EnteredAt, ss.ExitedAt, workStarted, ss.WorkStarted, in.ActorID); err != nil {
			return "", "", fmt.Errorf("repository.CreateImported: sesi riwayat: %w", err)
		}
	}

	for i := range in.Phases {
		ph := &in.Phases[i]
		if _, err := exec.Exec(ctx, `
			INSERT INTO task_pic_phases (task_id, status_id, user_id, is_active, acknowledged_at, activated_at, deactivated_at, assigned_by)
			VALUES ($1, $2, $3, $4, $5, $5, $6, $7)
		`, taskID, ph.StatusID, ph.UserID, ph.Active, ph.ActivatedAt, ph.DeactivatedAt, in.ActorID); err != nil {
			return "", "", fmt.Errorf("repository.CreateImported: PIC fase: %w", err)
		}
	}

	if err := insertTaskAudit(ctx, exec, in.ActorID, in.ActorRole, "task.created", taskID, in.WorkspaceID, nil,
		map[string]any{"title": in.Title, "task_code": taskCode, "imported": true, "status": in.StatusName, "with_history": len(in.Sessions) > 0}); err != nil {
		return "", "", fmt.Errorf("repository.CreateImported: audit: %w", err)
	}
	return taskID, taskCode, nil
}
