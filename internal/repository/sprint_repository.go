// Package repository -- SprintRepository (Task Management Core Phase 1,
// US-013; status 3-state + audit trail, Track S5 IG-92).
// "Hanya satu sprint aktif per project" ditegakkan di service layer
// (DATABASE_SCHEMA.md §5.14 catatan), bukan DB constraint.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type Sprint struct {
	ID        string
	ProjectID string
	// Code -- kode unik per project (SPR-01, ...); kunci penghubung import task.
	Code      string
	Name      string
	StartDate *time.Time
	EndDate   *time.Time
	Goal      *string
	Status    string // 'backlog' | 'active' | 'done'
	CreatedBy *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SprintRepository struct{}

func NewSprintRepository() *SprintRepository {
	return &SprintRepository{}
}

// Create menyimpan sprint baru + audit trail (IG-92, pola sama
// ProjectRepository.Create -- audit ditulis di titik yang sama dengan
// insert, satu transaksi).
func (r *SprintRepository) Create(ctx context.Context, exec db.Executor, projectID, code, name string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string) (*Sprint, error) {
	return r.insert(ctx, exec, projectID, code, name, "", startDate, endDate, goal, workspaceID, actorID, actorRole, false)
}

// CreateImported -- sprint dari import CSV: kode dan status ditentukan
// berkas (status "" = default backlog). Audit sprint.created ditandai
// imported=true.
func (r *SprintRepository) CreateImported(ctx context.Context, exec db.Executor, projectID, code, name, status string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string) (*Sprint, error) {
	return r.insert(ctx, exec, projectID, code, name, status, startDate, endDate, goal, workspaceID, actorID, actorRole, true)
}

func (r *SprintRepository) insert(ctx context.Context, exec db.Executor, projectID, code, name, status string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string, imported bool) (*Sprint, error) {
	var s Sprint
	s.ProjectID, s.Code, s.Name = projectID, code, name
	err := exec.QueryRow(ctx, `
		INSERT INTO sprints (project_id, code, name, start_date, end_date, goal, created_by, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(NULLIF($8, '')::sprint_status, 'backlog'))
		RETURNING id, status, created_at, updated_at
	`, projectID, code, name, startDate, endDate, goal, actorID, status).Scan(&s.ID, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.insert: %w", err)
	}
	s.StartDate, s.EndDate, s.Goal, s.CreatedBy = startDate, endDate, goal, &actorID

	state := map[string]any{"code": code, "name": name, "start_date": startDate, "end_date": endDate}
	if imported {
		state["imported"] = true
		state["status"] = s.Status
	}
	if err := insertSprintAudit(ctx, exec, actorID, actorRole, "sprint.created", s.ID, workspaceID, nil, state); err != nil {
		return nil, fmt.Errorf("repository.insert: audit: %w", err)
	}
	return &s, nil
}

// CodeTaken -- kode (case-insensitive) sudah dipakai sprint lain di project ini.
func (r *SprintRepository) CodeTaken(ctx context.Context, exec db.Executor, projectID, code string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sprints WHERE project_id = $1 AND upper(code) = upper($2))`, projectID, code).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.CodeTaken: %w", err)
	}
	return exists, nil
}

// NextAutoNumber -- angka berikutnya untuk kode otomatis "SPR-NN": angka
// terbesar dari kode berpola SPR-<angka> di project ini + 1 (kode kustom hasil
// import/input manual tidak ikut dihitung). Sprint 0 (SPR-00) -> berikutnya 1.
func (r *SprintRepository) NextAutoNumber(ctx context.Context, exec db.Executor, projectID string) (int, error) {
	var n int
	err := exec.QueryRow(ctx, `
		SELECT COALESCE(MAX(substring(code from '^SPR-([0-9]+)$')::int), 0) + 1 FROM sprints WHERE project_id = $1
	`, projectID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository.NextAutoNumber: %w", err)
	}
	return n, nil
}

// FormatSprintCode -- "SPR-00", "SPR-07", "SPR-123".
func FormatSprintCode(n int) string { return fmt.Sprintf("SPR-%02d", n) }

func (r *SprintRepository) List(ctx context.Context, exec db.Executor, projectID string) ([]Sprint, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, project_id, code, name, start_date, end_date, goal, status, created_by, created_at, updated_at
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
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Code, &s.Name, &s.StartDate, &s.EndDate, &s.Goal, &s.Status, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// NameTaken -- case-insensitive, dalam satu project, kecuali sprintID
// sendiri (dipakai Update). excludeID nil berarti tidak ada yang
// dikecualikan (dipakai Create) -- HARUS *string, bukan "" polos: "" tidak
// bisa di-cast ke uuid ("invalid input syntax for type uuid"), ditemukan
// lewat verifikasi live (bukan review kode) saat Create dipanggil.
func (r *SprintRepository) NameTaken(ctx context.Context, exec db.Executor, projectID, name string, excludeID *string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM sprints WHERE project_id = $1 AND lower(name) = lower($2) AND ($3::uuid IS NULL OR id != $3::uuid))
	`, projectID, name, excludeID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.NameTaken: %w", err)
	}
	return exists, nil
}

// CountInProject -- dipakai auto-generate nama "Sprint N" saat nama
// dikosongkan (pola sama PM Add Sprint.dc.html).
func (r *SprintRepository) CountInProject(ctx context.Context, exec db.Executor, projectID string) (int, error) {
	var count int
	err := exec.QueryRow(ctx, `SELECT COUNT(*) FROM sprints WHERE project_id = $1`, projectID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("repository.CountInProject: %w", err)
	}
	return count, nil
}

func (r *SprintRepository) Get(ctx context.Context, exec db.Executor, sprintID string) (*Sprint, error) {
	var s Sprint
	err := exec.QueryRow(ctx, `
		SELECT id, project_id, code, name, start_date, end_date, goal, status, created_by, created_at, updated_at
		FROM sprints WHERE id = $1
	`, sprintID).Scan(&s.ID, &s.ProjectID, &s.Code, &s.Name, &s.StartDate, &s.EndDate, &s.Goal, &s.Status, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrSprintNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &s, nil
}

// GetActiveInProject -- sprint berstatus 'active' di project ini, kalau
// ada (cuma boleh 0 atau 1, ditegakkan service layer).
func (r *SprintRepository) GetActiveInProject(ctx context.Context, exec db.Executor, projectID string) (*Sprint, error) {
	var s Sprint
	err := exec.QueryRow(ctx, `
		SELECT id, project_id, code, name, start_date, end_date, goal, status, created_by, created_at, updated_at
		FROM sprints WHERE project_id = $1 AND status = 'active'
	`, projectID).Scan(&s.ID, &s.ProjectID, &s.Code, &s.Name, &s.StartDate, &s.EndDate, &s.Goal, &s.Status, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("repository.GetActiveInProject: %w", err)
	}
	return &s, nil
}

func (r *SprintRepository) Update(ctx context.Context, exec db.Executor, sprintID, name string, startDate, endDate *time.Time, goal *string, workspaceID, actorID, actorRole string, before map[string]any) error {
	tag, err := exec.Exec(ctx, `
		UPDATE sprints SET name = $2, start_date = $3, end_date = $4, goal = $5, updated_at = NOW()
		WHERE id = $1
	`, sprintID, name, startDate, endDate, goal)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrSprintNotFound)
	}
	if err := insertSprintAudit(ctx, exec, actorID, actorRole, "sprint.updated", sprintID, workspaceID, before,
		map[string]any{"name": name, "start_date": startDate, "end_date": endDate}); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// SetStatus -- transisi status + audit ("sprint.started"/"sprint.completed"
// /"sprint.reopened" ditentukan caller lewat parameter action).
func (r *SprintRepository) SetStatus(ctx context.Context, exec db.Executor, sprintID, status, action, workspaceID, actorID, actorRole, fromStatus string) error {
	tag, err := exec.Exec(ctx, `UPDATE sprints SET status = $2, updated_at = NOW() WHERE id = $1`, sprintID, status)
	if err != nil {
		return fmt.Errorf("repository.SetStatus: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetStatus: %w", domain.ErrSprintNotFound)
	}
	if err := insertSprintAudit(ctx, exec, actorID, actorRole, action, sprintID, workspaceID,
		map[string]any{"status": fromStatus}, map[string]any{"status": status}); err != nil {
		return fmt.Errorf("repository.SetStatus: audit: %w", err)
	}
	return nil
}

func (r *SprintRepository) Delete(ctx context.Context, exec db.Executor, sprintID, workspaceID, actorID, actorRole, name string) error {
	tag, err := exec.Exec(ctx, `DELETE FROM sprints WHERE id = $1`, sprintID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrSprintNotFound)
	}
	if err := insertSprintAudit(ctx, exec, actorID, actorRole, "sprint.deleted", sprintID, workspaceID,
		map[string]any{"name": name}, nil); err != nil {
		return fmt.Errorf("repository.Delete: audit: %w", err)
	}
	return nil
}

// UnassignIncompleteTasks -- dipanggil saat sprint di-complete (S4-09):
// task yang belum DONE dipindah ke backlog (sprint_id NULL), bukan
// otomatis ke sprint berikutnya (tidak ada urutan sprint eksplisit di
// skema -- GA/PM memindah manual kalau perlu, dijelaskan di FE).
func (r *SprintRepository) UnassignIncompleteTasks(ctx context.Context, exec db.Executor, sprintID string, closedStatusIDs []string) error {
	_, err := exec.Exec(ctx, `
		UPDATE tasks SET sprint_id = NULL, updated_at = NOW()
		WHERE sprint_id = $1 AND status_id <> ALL($2::uuid[]) AND deleted_at IS NULL
	`, sprintID, closedStatusIDs)
	if err != nil {
		return fmt.Errorf("repository.UnassignIncompleteTasks: %w", err)
	}
	return nil
}

// Summary -- GET /sprints/:id/summary (Phase 4, US-018a/S4-59; diperluas
// IG-92 US-081 kapasitas SP: total/selesai/tersisa). Task CANCELED tidak
// dihitung sama sekali (IG-118). "Selesai" = task
// berstatus custom_statuses.name='DONE' (JOIN, sama definisi dipakai
// SprintService.closeSprint mencari doneStatusID).
func (r *SprintRepository) Summary(ctx context.Context, exec db.Executor, sprintID string) (totalSP, doneSP, unestimatedCount, taskCount int, err error) {
	err = exec.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(t.story_points), 0),
			COALESCE(SUM(t.story_points) FILTER (WHERE cs.name = 'DONE'), 0),
			COUNT(*) FILTER (WHERE t.story_points IS NULL),
			COUNT(*)
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.sprint_id = $1 AND t.deleted_at IS NULL AND cs.name <> 'CANCELED'
	`, sprintID).Scan(&totalSP, &doneSP, &unestimatedCount, &taskCount)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("repository.Summary: %w", err)
	}
	return totalSP, doneSP, unestimatedCount, taskCount, nil
}

// AssignTasks -- "Tarik Task dari Backlog" saat buat sprint baru (PM Add
// Sprint.dc.html). Hanya task milik project yang sama yang diproses
// (WHERE project_id, defense-in-depth kalau taskIDs dari klien tercampur
// project lain), silently diabaikan bukan error -- pola sama repository
// lain yang menerima banyak ID sekaligus.
func (r *SprintRepository) AssignTasks(ctx context.Context, exec db.Executor, sprintID, projectID, workspaceID, actorID, actorRole string, taskIDs []string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		UPDATE tasks SET sprint_id = $1, updated_at = NOW()
		WHERE id = ANY($2) AND project_id = $3 AND deleted_at IS NULL
	`, sprintID, taskIDs, projectID)
	if err != nil {
		return fmt.Errorf("repository.AssignTasks: %w", err)
	}
	if err := insertSprintAudit(ctx, exec, actorID, actorRole, "sprint.tasks_assigned", sprintID, workspaceID, nil,
		map[string]any{"task_count": len(taskIDs)}); err != nil {
		return fmt.Errorf("repository.AssignTasks: audit: %w", err)
	}
	return nil
}

// insertSprintAudit -- pola sama insertCustomStatusAudit/insertProjectAudit
// (single chokepoint), entity_type 'sprint'. US-013 AC: "Semua perubahan
// sprint dicatat di audit trail" -- sebelumnya TIDAK PERNAH ditulis sama
// sekali (ditemukan IG-92 saat audit sebelum implementasi, bukan regresi).
func insertSprintAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, sprintID, workspaceID string, stateBefore, stateAfter map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{}
	if path != "" {
		metadata["request_path"] = path
	}
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertSprintAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertSprintAudit: encode state_after: %w", err)
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertSprintAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'sprint', $4, $5, $6::inet, $7, $8, $9)
	`, actorID, actorRole, action, sprintID, workspaceID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}

// NextAutoSprintName -- "Sprint N" berikutnya (N = jumlah sprint existing
// + 1), dipakai saat nama dikosongkan (pola sama PM Add Sprint.dc.html
// nextName()). Tidak menjamin unik 100% (mis. sprint lama pernah di-rename
// manual jadi "Sprint 3" duplikat) -- service tetap menjalankan NameTaken
// setelahnya sebagai jaring pengaman.
func NextAutoSprintName(count int) string {
	return "Sprint " + strconv.Itoa(count+1)
}
