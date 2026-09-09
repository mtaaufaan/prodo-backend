// Package repository -- SprintRepository (Task Management Core Phase 1,
// US-013). "Hanya satu sprint aktif per project" ditegakkan di service
// layer (DATABASE_SCHEMA.md §5.14 catatan), bukan DB constraint.
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

type Sprint struct {
	ID        string
	ProjectID string
	Name      string
	StartDate *time.Time
	EndDate   *time.Time
	IsActive  bool
	CreatedBy *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SprintRepository struct{}

func NewSprintRepository() *SprintRepository {
	return &SprintRepository{}
}

func (r *SprintRepository) Create(ctx context.Context, exec db.Executor, projectID, name string, startDate, endDate *time.Time, createdBy string) (*Sprint, error) {
	var s Sprint
	s.ProjectID, s.Name = projectID, name
	err := exec.QueryRow(ctx, `
		INSERT INTO sprints (project_id, name, start_date, end_date, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, is_active, created_at, updated_at
	`, projectID, name, startDate, endDate, createdBy).Scan(&s.ID, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	s.StartDate, s.EndDate, s.CreatedBy = startDate, endDate, &createdBy
	return &s, nil
}

func (r *SprintRepository) List(ctx context.Context, exec db.Executor, projectID string) ([]Sprint, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, project_id, name, start_date, end_date, is_active, created_by, created_at, updated_at
		FROM sprints WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	list := make([]Sprint, 0)
	for rows.Next() {
		var s Sprint
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.StartDate, &s.EndDate, &s.IsActive, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (r *SprintRepository) Get(ctx context.Context, exec db.Executor, sprintID string) (*Sprint, error) {
	var s Sprint
	err := exec.QueryRow(ctx, `
		SELECT id, project_id, name, start_date, end_date, is_active, created_by, created_at, updated_at
		FROM sprints WHERE id = $1
	`, sprintID).Scan(&s.ID, &s.ProjectID, &s.Name, &s.StartDate, &s.EndDate, &s.IsActive, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrSprintNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &s, nil
}

func (r *SprintRepository) Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time) error {
	tag, err := exec.Exec(ctx, `
		UPDATE sprints SET name = $2, start_date = $3, end_date = $4, updated_at = NOW()
		WHERE id = $1
	`, sprintID, name, startDate, endDate)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrSprintNotFound)
	}
	return nil
}

// DeactivateAllInProject -- dipanggil SEBELUM mengaktifkan sprint lain,
// menegakkan "hanya satu sprint aktif per project" (S4-08).
func (r *SprintRepository) DeactivateAllInProject(ctx context.Context, exec db.Executor, projectID string) error {
	_, err := exec.Exec(ctx, `UPDATE sprints SET is_active = FALSE, updated_at = NOW() WHERE project_id = $1 AND is_active = TRUE`, projectID)
	if err != nil {
		return fmt.Errorf("repository.DeactivateAllInProject: %w", err)
	}
	return nil
}

func (r *SprintRepository) SetActive(ctx context.Context, exec db.Executor, sprintID string, active bool) error {
	tag, err := exec.Exec(ctx, `UPDATE sprints SET is_active = $2, updated_at = NOW() WHERE id = $1`, sprintID, active)
	if err != nil {
		return fmt.Errorf("repository.SetActive: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetActive: %w", domain.ErrSprintNotFound)
	}
	return nil
}

func (r *SprintRepository) Delete(ctx context.Context, exec db.Executor, sprintID string) error {
	tag, err := exec.Exec(ctx, `DELETE FROM sprints WHERE id = $1`, sprintID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrSprintNotFound)
	}
	return nil
}

// UnassignIncompleteTasks -- dipanggil saat sprint di-complete (S4-09):
// task yang belum DONE dipindah ke backlog (sprint_id NULL), bukan
// otomatis ke sprint berikutnya (tidak ada urutan sprint eksplisit di
// skema -- GA/PM memindah manual kalau perlu, dijelaskan di FE).
func (r *SprintRepository) UnassignIncompleteTasks(ctx context.Context, exec db.Executor, sprintID, doneStatusID string) error {
	_, err := exec.Exec(ctx, `
		UPDATE tasks SET sprint_id = NULL, updated_at = NOW()
		WHERE sprint_id = $1 AND status_id != $2 AND deleted_at IS NULL
	`, sprintID, doneStatusID)
	if err != nil {
		return fmt.Errorf("repository.UnassignIncompleteTasks: %w", err)
	}
	return nil
}

// Summary -- GET /sprints/:id/summary (Phase 4, US-018a/S4-59):
// total_story_points (NULL dihitung 0) + unestimated_count.
func (r *SprintRepository) Summary(ctx context.Context, exec db.Executor, sprintID string) (totalStoryPoints, unestimatedCount int, err error) {
	err = exec.QueryRow(ctx, `
		SELECT COALESCE(SUM(story_points), 0), COUNT(*) FILTER (WHERE story_points IS NULL)
		FROM tasks WHERE sprint_id = $1 AND deleted_at IS NULL
	`, sprintID).Scan(&totalStoryPoints, &unestimatedCount)
	if err != nil {
		return 0, 0, fmt.Errorf("repository.Summary: %w", err)
	}
	return totalStoryPoints, unestimatedCount, nil
}
