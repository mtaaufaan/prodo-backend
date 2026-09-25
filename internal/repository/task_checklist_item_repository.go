// Package repository -- TaskChecklistItemRepository (SUB-TASK, "PM Task
// Detail.dc.html", IG-97 susulan). Lihat komentar migrasi
// 20261030090000_task_checklist_items kenapa ini tabel baru ringan,
// bukan reuse tasks.parent_task_id.
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type TaskChecklistItem struct {
	ID        string
	TaskID    string
	Title     string
	IsDone    bool
	Position  float64
	CreatedBy *string
}

type TaskChecklistItemRepository struct{}

func NewTaskChecklistItemRepository() *TaskChecklistItemRepository {
	return &TaskChecklistItemRepository{}
}

func (r *TaskChecklistItemRepository) Create(ctx context.Context, exec db.Executor, taskID, title, createdBy string) (*TaskChecklistItem, error) {
	item := &TaskChecklistItem{TaskID: taskID, Title: title, CreatedBy: &createdBy}
	err := exec.QueryRow(ctx, `
		INSERT INTO task_checklist_items (task_id, title, created_by, position)
		VALUES ($1, $2, $3, COALESCE((SELECT MAX(position) FROM task_checklist_items WHERE task_id = $1), 0) + 1)
		RETURNING id, position
	`, taskID, title, createdBy).Scan(&item.ID, &item.Position)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	return item, nil
}

func (r *TaskChecklistItemRepository) ListForTask(ctx context.Context, exec db.Executor, taskID string) ([]TaskChecklistItem, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, task_id, title, is_done, position, created_by
		FROM task_checklist_items WHERE task_id = $1 ORDER BY position ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TaskChecklistItem, 0)
	for rows.Next() {
		var item TaskChecklistItem
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Title, &item.IsDone, &item.Position, &item.CreatedBy); err != nil {
			return nil, fmt.Errorf("repository.ListForTask: scan: %w", err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	return list, nil
}

// GetTaskID -- resolusi task_id milik satu item, dipakai service untuk
// invalidasi/otorisasi tanpa butuh param taskID dari handler (route
// PATCH/DELETE item cuma py :itemId, bukan :taskId).
func (r *TaskChecklistItemRepository) GetTaskID(ctx context.Context, exec db.Executor, itemID string) (string, error) {
	var taskID string
	err := exec.QueryRow(ctx, `SELECT task_id FROM task_checklist_items WHERE id = $1`, itemID).Scan(&taskID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("repository.GetTaskID: %w", domain.ErrTaskNotFound)
		}
		return "", fmt.Errorf("repository.GetTaskID: %w", err)
	}
	return taskID, nil
}

// Update -- title/isDone opsional (nil pointer = tidak diubah), dipanggil
// PATCH untuk rename ATAU toggle checkbox secara independen.
func (r *TaskChecklistItemRepository) Update(ctx context.Context, exec db.Executor, itemID string, title *string, isDone *bool) error {
	tag, err := exec.Exec(ctx, `
		UPDATE task_checklist_items
		SET title = COALESCE($2, title), is_done = COALESCE($3, is_done), updated_at = NOW()
		WHERE id = $1
	`, itemID, title, isDone)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrTaskNotFound)
	}
	return nil
}

func (r *TaskChecklistItemRepository) Delete(ctx context.Context, exec db.Executor, itemID string) error {
	tag, err := exec.Exec(ctx, `DELETE FROM task_checklist_items WHERE id = $1`, itemID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrTaskNotFound)
	}
	return nil
}
