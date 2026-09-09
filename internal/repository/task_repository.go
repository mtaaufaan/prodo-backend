// Package repository -- TaskRepository (Task Management Core Phase 1,
// US-014; completeness+is_blocked Phase 3, US-017c/018). Story-point/
// time-tracking enforcement penuh masih Phase 4 -- lihat komentar migrasi
// 20260924090000_task_core_phase1.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type Task struct {
	ID             string
	ProjectID      string
	SprintID       *string
	SprintName     *string
	ParentTaskID   *string
	StatusID       string
	StatusName     string
	StatusColor    *string
	Title          string
	Description    json.RawMessage
	Priority       string
	Completeness   *string
	DueDate        *time.Time
	EstimatedHours *float64
	StoryPoints    *int
	TaskCode       *string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	IsBlocked      bool
	Assignees      []TaskAssignee
}

type TaskAssignee struct {
	UserID      string
	DisplayName string
	Email       string
	Role        string
}

// TaskFilter -- field kosong/nil berarti tidak difilter (S4-14 AC).
type TaskFilter struct {
	StatusID   string
	Priority   string
	SprintID   string
	AssigneeID string
}

type TaskRepository struct{}

func NewTaskRepository() *TaskRepository {
	return &TaskRepository{}
}

// nextTaskCode -- "{project.code}-{seq:03d}" menggunakan project_code
// yang sudah ada (DATABASE_SCHEMA.md §5.12: "Prefiks nomor task"),
// BUKAN format "TSK-{SPRINT_NO}{SEQ}" di komentar §5.15 -- project.code
// sudah dibangun tepat untuk tujuan ini sejak S3, dipakai apa adanya
// (reuse) alih-alih menciptakan skema penomoran kedua yang tumpang tindih.
func (r *TaskRepository) nextTaskCode(ctx context.Context, exec db.Executor, projectID string) (string, error) {
	var code *string
	var count int
	err := exec.QueryRow(ctx, `SELECT code FROM projects WHERE id = $1`, projectID).Scan(&code)
	if err != nil {
		return "", fmt.Errorf("nextTaskCode: ambil project code: %w", err)
	}
	if err := exec.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE project_id = $1`, projectID).Scan(&count); err != nil {
		return "", fmt.Errorf("nextTaskCode: hitung task: %w", err)
	}
	prefix := "TSK"
	if code != nil && *code != "" {
		prefix = *code
	}
	return fmt.Sprintf("%s-%03d", prefix, count+1), nil
}

func (r *TaskRepository) Create(ctx context.Context, exec db.Executor, projectID string, sprintID *string, statusID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, createdBy string, assigneeUserIDs []string) (*Task, error) {
	taskCode, err := r.nextTaskCode(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}

	var id string
	var createdAt, updatedAt time.Time
	err = exec.QueryRow(ctx, `
		INSERT INTO tasks (project_id, sprint_id, status_id, title, description, priority, completeness, due_date, estimated_hours, story_points, task_code, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, 'incomplete', $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at
	`, projectID, sprintID, statusID, title, description, priority, dueDate, estimatedHours, storyPoints, taskCode, createdBy).Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}

	for _, userID := range assigneeUserIDs {
		if _, err := exec.Exec(ctx, `
			INSERT INTO task_assignees (task_id, user_id, assignee_role, assigned_by)
			VALUES ($1, $2, 'contributor', $3)
		`, id, userID, createdBy); err != nil {
			return nil, fmt.Errorf("repository.Create: assignee: %w", err)
		}
	}

	// Task Management Core Phase 2 (S4-30): pembuat task otomatis menjadi
	// PIC fase awal (BACKLOG) -- INSERT langsung (bukan lewat
	// TaskPicRepository, sama package, hindari repo-panggil-repo).
	if _, err := exec.Exec(ctx, `
		INSERT INTO task_pic_phases (task_id, status_id, user_id, assigned_by)
		VALUES ($1, $2, $3, $3)
	`, id, statusID, createdBy); err != nil {
		return nil, fmt.Errorf("repository.Create: pic fase awal: %w", err)
	}

	return r.Get(ctx, exec, id)
}

// isBlockedSubquery -- Phase 3 (US-018/S4-53): task terblokir kalau ada
// predecessor yang BELUM DONE. Subquery ter-index (idx_task_dependencies_successor),
// dievaluasi native di DB -- bukan N+1 query per task dari Go.
const isBlockedSubquery = `
	EXISTS (
		SELECT 1 FROM task_dependencies td
		JOIN tasks tp ON tp.id = td.predecessor_id
		JOIN custom_statuses cs_p ON cs_p.id = tp.status_id
		WHERE td.successor_id = t.id AND cs_p.name != 'DONE'
	)
`

const taskSelectColumns = `
	t.id, t.project_id, t.sprint_id, s.name, t.parent_task_id, t.status_id, cs.name, cs.color_token,
	t.title, t.description, t.priority, t.completeness, t.due_date, t.estimated_hours, t.story_points,
	t.task_code, t.created_by, t.created_at, t.updated_at, t.completed_at, ` + isBlockedSubquery + `

`

func scanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	if err := row.Scan(&t.ID, &t.ProjectID, &t.SprintID, &t.SprintName, &t.ParentTaskID, &t.StatusID, &t.StatusName, &t.StatusColor,
		&t.Title, &t.Description, &t.Priority, &t.Completeness, &t.DueDate, &t.EstimatedHours, &t.StoryPoints,
		&t.TaskCode, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &t.IsBlocked); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TaskRepository) Get(ctx context.Context, exec db.Executor, taskID string) (*Task, error) {
	row := exec.QueryRow(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		WHERE t.id = $1 AND t.deleted_at IS NULL
	`, taskID)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrTaskNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	assignees, err := r.listAssignees(ctx, exec, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	t.Assignees = assignees
	return t, nil
}

func (r *TaskRepository) listAssignees(ctx context.Context, exec db.Executor, taskID string) ([]TaskAssignee, error) {
	rows, err := exec.Query(ctx, `
		SELECT ta.user_id, u.display_name, u.email, ta.assignee_role
		FROM task_assignees ta
		JOIN users u ON u.id = ta.user_id
		WHERE ta.task_id = $1
		ORDER BY ta.assigned_at
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("listAssignees: %w", err)
	}
	defer rows.Close()

	list := make([]TaskAssignee, 0)
	for rows.Next() {
		var a TaskAssignee
		if err := rows.Scan(&a.UserID, &a.DisplayName, &a.Email, &a.Role); err != nil {
			return nil, fmt.Errorf("listAssignees: scan: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// List -- filter status/priority/sprint/assignee, tanpa paginasi server
// (S4-14 AC minta pagination, tapi jumlah task per project realistis kecil
// di v1 -- pola sama Import Data/Webhook list, paginasi klien di FE kalau
// perlu). Assignee TIDAK di-load per baris di sini (N+1 query) -- FE board
// cukup nama assignee pertama, dipakai query terpisah agregat bila perlu.
func (r *TaskRepository) List(ctx context.Context, exec db.Executor, projectID string, f TaskFilter) ([]Task, error) {
	clauses := []string{"t.project_id = $1", "t.deleted_at IS NULL"}
	args := []any{projectID}
	n := 1
	if f.StatusID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("t.status_id = $%d", n))
		args = append(args, f.StatusID)
	}
	if f.Priority != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("t.priority = $%d", n))
		args = append(args, f.Priority)
	}
	if f.SprintID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("t.sprint_id = $%d", n))
		args = append(args, f.SprintID)
	}
	if f.AssigneeID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = t.id AND ta.user_id = $%d)", n))
		args = append(args, f.AssigneeID)
	}
	where := clauses[0]
	for _, c := range clauses[1:] {
		where += " AND " + c
	}

	rows, err := exec.Query(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		WHERE `+where+`
		ORDER BY t.created_at DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	list := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}

	// Assignee per task -- satu query agregat, bukan N+1 (board menampilkan
	// nama assignee per kartu).
	if len(list) > 0 {
		ids := make([]string, len(list))
		byID := make(map[string]*Task, len(list))
		for i := range list {
			ids[i] = list[i].ID
			byID[list[i].ID] = &list[i]
		}
		aRows, err := exec.Query(ctx, `
			SELECT ta.task_id, ta.user_id, u.display_name, u.email, ta.assignee_role
			FROM task_assignees ta
			JOIN users u ON u.id = ta.user_id
			WHERE ta.task_id = ANY($1)
			ORDER BY ta.assigned_at
		`, ids)
		if err != nil {
			return nil, fmt.Errorf("repository.List: assignees: %w", err)
		}
		defer aRows.Close()
		for aRows.Next() {
			var taskID string
			var a TaskAssignee
			if err := aRows.Scan(&taskID, &a.UserID, &a.DisplayName, &a.Email, &a.Role); err != nil {
				return nil, fmt.Errorf("repository.List: assignees scan: %w", err)
			}
			if t, ok := byID[taskID]; ok {
				t.Assignees = append(t.Assignees, a)
			}
		}
	}
	return list, nil
}

func (r *TaskRepository) Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE tasks SET title = $2, description = $3, priority = $4, due_date = $5,
		       estimated_hours = $6, story_points = $7, sprint_id = $8, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID, title, description, priority, dueDate, estimatedHours, storyPoints, sprintID)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrTaskNotFound)
	}
	return nil
}

// SetStatus -- ganti status DASAR (Phase 1, tanpa PIC Handoff/dependency
// hard-block -- lihat komentar package). completed_at diisi/dikosongkan
// otomatis berdasarkan apakah status tujuan bernama DONE.
func (r *TaskRepository) SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool) error {
	var completedAt any
	if isDone {
		completedAt = time.Now()
	}
	tag, err := exec.Exec(ctx, `
		UPDATE tasks SET status_id = $2, completed_at = $3, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID, statusID, completedAt)
	if err != nil {
		return fmt.Errorf("repository.SetStatus: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetStatus: %w", domain.ErrTaskNotFound)
	}
	return nil
}

// SetCompleteness -- Phase 3 (US-017c, S4-44): toggle "Lengkap/Belum
// Lengkap", dipanggil setelah service memverifikasi actor = pembuat task
// atau PIC aktif.
func (r *TaskRepository) SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE tasks SET completeness = $2, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID, completeness)
	if err != nil {
		return fmt.Errorf("repository.SetCompleteness: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetCompleteness: %w", domain.ErrTaskNotFound)
	}
	return nil
}

func (r *TaskRepository) SoftDelete(ctx context.Context, exec db.Executor, taskID string) error {
	tag, err := exec.Exec(ctx, `UPDATE tasks SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, taskID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrTaskNotFound)
	}
	return nil
}

func (r *TaskRepository) GetProjectID(ctx context.Context, exec db.Executor, taskID string) (string, error) {
	var projectID string
	err := exec.QueryRow(ctx, `SELECT project_id FROM tasks WHERE id = $1 AND deleted_at IS NULL`, taskID).Scan(&projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.GetProjectID: %w", domain.ErrTaskNotFound)
		}
		return "", fmt.Errorf("repository.GetProjectID: %w", err)
	}
	return projectID, nil
}
