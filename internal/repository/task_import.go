package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// TaskImportInput -- satu task dari import CSV (IG-120 tahap b). Status awal
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
}

// CreateImported -- jalur KHUSUS import (bukan TaskRepository.Create): data lama
// dimigrasi apa adanya, jadi
//   - status awal sesuai berkas, sesi status pertama dibuka di status itu;
//   - TIDAK ada PIC fase awal otomatis atas nama pembuat (Create memberi
//     pelaku PIC BACKLOG; di sini pelaku hanya pengimpor, bukan penanggung
//     jawab task) -- PIC per status menyusul di tahap (c);
//   - completeness: BACKLOG 'incomplete' (sama task manual, PM menandai
//     Lengkap), status lain 'complete' (kalau tidak, kartu menampilkan
//     "BELUM LENGKAP" padahal task sudah berjalan);
//   - DONE -> completed_at = NOW() (tanggal asli menyusul tahap c);
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
	if in.StatusName == "DONE" {
		completedAt = time.Now()
	}
	err = exec.QueryRow(ctx, `
		INSERT INTO tasks (project_id, sprint_id, status_id, title, priority, completeness, start_date, due_date, estimated_hours, story_points, task_code, created_by, completed_at, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			COALESCE((SELECT MAX(position) FROM tasks WHERE project_id = $1 AND status_id = $3), 0) + 1)
		RETURNING id
	`, in.ProjectID, in.SprintID, in.StatusID, in.Title, in.Priority, completeness, in.StartDate, in.DueDate, in.EstimatedHours, in.StoryPoints,
		taskCode, in.ActorID, completedAt).Scan(&taskID)
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
	if _, err := exec.Exec(ctx, `
		INSERT INTO task_status_sessions (task_id, status_id, session_no, triggered_by)
		VALUES ($1, $2, 1, $3)
	`, taskID, in.StatusID, in.ActorID); err != nil {
		return "", "", fmt.Errorf("repository.CreateImported: sesi status awal: %w", err)
	}
	if err := insertTaskAudit(ctx, exec, in.ActorID, in.ActorRole, "task.created", taskID, in.WorkspaceID, nil,
		map[string]any{"title": in.Title, "task_code": taskCode, "imported": true, "status": in.StatusName}); err != nil {
		return "", "", fmt.Errorf("repository.CreateImported: audit: %w", err)
	}
	return taskID, taskCode, nil
}
