// Package repository -- TaskRepository (Task Management Core Phase 1,
// US-014; completeness+is_blocked Phase 3, US-017c/018; regression_count
// Phase 4, US-018c). Lihat komentar migrasi 20260924090000_task_core_phase1.
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
	ID              string
	ProjectID       string
	SprintID        *string
	SprintName      *string
	ParentTaskID    *string
	StatusID        string
	StatusName      string
	StatusColor     *string
	Title           string
	Description     json.RawMessage
	Priority        string
	Completeness    *string
	DueDate         *time.Time
	EstimatedHours  *float64
	StoryPoints     *int
	TaskCode        *string
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
	IsBlocked       bool
	RegressionCount int
	Position        float64
	Assignees       []TaskAssignee
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

// parentTaskID (susulan S4W-11, action "Buat sub-task otomatis") -- kolom
// `parent_task_id` sudah ada di struct `Task`/skema sejak awal tapi TIDAK
// PERNAH diisi Create manapun sampai sekarang -- satu-satunya konsumen saat
// ini adalah RuleService lewat TaskService.CreateSubtaskForRule, nil untuk
// alur create task manusia biasa.
func (r *TaskRepository) Create(ctx context.Context, exec db.Executor, projectID string, sprintID, parentTaskID *string, statusID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, createdBy string, assigneeUserIDs []string, actorRole, workspaceID string) (*Task, error) {
	taskCode, err := r.nextTaskCode(ctx, exec, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}

	var id string
	var createdAt, updatedAt time.Time
	err = exec.QueryRow(ctx, `
		INSERT INTO tasks (project_id, sprint_id, parent_task_id, status_id, title, description, priority, completeness, due_date, estimated_hours, story_points, task_code, created_by, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'incomplete', $8, $9, $10, $11, $12,
			COALESCE((SELECT MAX(position) FROM tasks WHERE project_id = $1 AND status_id = $4), 0) + 1)
		RETURNING id, created_at, updated_at
	`, projectID, sprintID, parentTaskID, statusID, title, description, priority, dueDate, estimatedHours, storyPoints, taskCode, createdBy).Scan(&id, &createdAt, &updatedAt)
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

	// Task Management Core Phase 4 (S4-62): sesi status pertama (BACKLOG)
	// dibuka langsung saat task dibuat -- INSERT langsung (bukan lewat
	// TaskStatusSessionRepository, sama package, hindari repo-panggil-repo).
	if _, err := exec.Exec(ctx, `
		INSERT INTO task_status_sessions (task_id, status_id, session_no, triggered_by)
		VALUES ($1, $2, 1, $3)
	`, id, statusID, createdBy); err != nil {
		return nil, fmt.Errorf("repository.Create: sesi status awal: %w", err)
	}

	if err := insertTaskAudit(ctx, exec, createdBy, actorRole, "task.created", id, workspaceID, nil,
		map[string]any{"title": title, "task_code": taskCode}); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
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

// regressionCountSubquery -- Phase 4 (US-018c/S4-69): jumlah sesi regresi
// task ini, ter-index (idx_task_status_sessions_regression), dievaluasi
// native di DB -- pola sama isBlockedSubquery di atas.
const regressionCountSubquery = `
	(SELECT COUNT(*) FROM task_status_sessions tss WHERE tss.task_id = t.id AND tss.is_regression = TRUE)
`

const taskSelectColumns = `
	t.id, t.project_id, t.sprint_id, s.name, t.parent_task_id, t.status_id, cs.name, cs.color_token,
	t.title, t.description, t.priority, t.completeness, t.due_date, t.estimated_hours, t.story_points,
	t.task_code, t.created_by, t.created_at, t.updated_at, t.completed_at, ` + isBlockedSubquery + `,
	` + regressionCountSubquery + `, t.position
`

func scanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	if err := row.Scan(&t.ID, &t.ProjectID, &t.SprintID, &t.SprintName, &t.ParentTaskID, &t.StatusID, &t.StatusName, &t.StatusColor,
		&t.Title, &t.Description, &t.Priority, &t.Completeness, &t.DueDate, &t.EstimatedHours, &t.StoryPoints,
		&t.TaskCode, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &t.IsBlocked, &t.RegressionCount, &t.Position); err != nil {
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
		ORDER BY t.status_id, t.position
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

func (r *TaskRepository) Update(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, priority string, dueDate *time.Time, estimatedHours *float64, storyPoints *int, sprintID *string, actorID, actorRole, workspaceID string) error {
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
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.updated", taskID, workspaceID, nil,
		map[string]any{"title": title, "priority": priority}); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// SetStatus -- ganti status DASAR (Phase 1, tanpa PIC Handoff/dependency
// hard-block -- lihat komentar package). completed_at diisi/dikosongkan
// otomatis berdasarkan apakah status tujuan bernama DONE.
func (r *TaskRepository) SetStatus(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool, actorID, actorRole, workspaceID, statusBefore, statusAfter string) error {
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
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.status_changed", taskID, workspaceID,
		map[string]any{"status": statusBefore}, map[string]any{"status": statusAfter}); err != nil {
		return fmt.Errorf("repository.SetStatus: audit: %w", err)
	}
	return nil
}

// SetPosition -- drag-reorder kartu dalam satu kolom Kanban (Track S5,
// desain "PM Board.dc.html" moveTaskOrder/taskRank). Nilai posisi baru
// dihitung service layer (titik tengah dua tetangga, fractional indexing)
// -- repository cuma menyimpan apa adanya.
func (r *TaskRepository) SetPosition(ctx context.Context, exec db.Executor, taskID string, position float64) error {
	tag, err := exec.Exec(ctx, `UPDATE tasks SET position = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, taskID, position)
	if err != nil {
		return fmt.Errorf("repository.SetPosition: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetPosition: %w", domain.ErrTaskNotFound)
	}
	return nil
}

// SetCompleteness -- Phase 3 (US-017c, S4-44): toggle "Lengkap/Belum
// Lengkap", dipanggil setelah service memverifikasi actor = pembuat task
// atau PIC aktif.
func (r *TaskRepository) SetCompleteness(ctx context.Context, exec db.Executor, taskID, completeness, actorID, actorRole, workspaceID string) error {
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
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.completeness_changed", taskID, workspaceID, nil,
		map[string]any{"completeness": completeness}); err != nil {
		return fmt.Errorf("repository.SetCompleteness: audit: %w", err)
	}
	return nil
}

func (r *TaskRepository) SoftDelete(ctx context.Context, exec db.Executor, taskID, actorID, actorRole, workspaceID string) error {
	tag, err := exec.Exec(ctx, `UPDATE tasks SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, taskID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrTaskNotFound)
	}
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.deleted", taskID, workspaceID, nil, nil); err != nil {
		return fmt.Errorf("repository.SoftDelete: audit: %w", err)
	}
	return nil
}

// AssignUser (S4W-11, action rule "Assign ke user") -- idempotent, sama
// pola INSERT assignee di Create. Trigger "assignee_changed" (US-049 AC)
// TETAP DORMANT -- ini satu-satunya jalur assign yang ada di codebase
// (tidak ada fitur reassign task manual di luar rule), dan rule-triggered
// action ini SENGAJA tidak balik memicu evaluasi rule (lihat komentar
// TaskService.AssignUserForRule) -- jadi trigger itu tidak pernah benar-
// benar terpicu sampai ada fitur reassign manusia. Sama nasib comment_added.
func (r *TaskRepository) AssignUser(ctx context.Context, exec db.Executor, taskID, userID, assignedBy string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO task_assignees (task_id, user_id, assignee_role, assigned_by)
		VALUES ($1, $2, 'contributor', $3)
		ON CONFLICT (task_id, user_id) DO NOTHING
	`, taskID, userID, assignedBy)
	if err != nil {
		return fmt.Errorf("repository.AssignUser: %w", err)
	}
	return nil
}

// insertTaskAudit -- IG-94/IG-97, pola SAMA PERSIS insertSprintAudit
// (sprint_repository.go): satu chokepoint INSERT ke audit_logs per entity
// (entity_type='task'), actor_ip+request_path dari requestMetaFromContext
// (middleware.RequestMeta, TIDAK perlu parameter tambahan), workspace_id
// kolom asli (task selalu tahu workspace-nya lewat project). actorRole DI
// SINI HARUS role hasil resolve dari service (TaskService.authorize/
// resolveRole), BUKAN parameter actorRole mentah dari handler -- rute task
// sengaja tanpa middleware RequireRole (route berbasis :projectId), jadi
// actorRole mentah SELALU string kosong (bug yang sama seperti IG-92 kalau
// tidak diperbaiki di titik ini).
func insertTaskAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, taskID, workspaceID string, stateBefore, stateAfter map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{}
	if path != "" {
		metadata["request_path"] = path
	}
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertTaskAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertTaskAudit: encode state_after: %w", err)
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertTaskAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'task', $4, $5, $6::inet, $7, $8, $9)
	`, actorID, actorRole, action, taskID, workspaceID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}

// AuditEntry -- satu baris feed AKTIVITAS (IG-97 tab AKTIVITAS), dibaca
// balik dari audit_logs entity_type='task'. Resolusi nama actor dilakukan
// di layer service/handler (JOIN users di query ListAudit langsung, sama
// pola ringan seperti WorkspaceAuditRepository -- task TIDAK py masalah
// hard-delete seperti webhook_configs, jadi live JOIN aman dipakai apa
// adanya tanpa snapshot immutable tambahan).
type AuditEntry struct {
	ID          string
	Action      string
	ActorID     *string
	ActorName   string
	ActorEmail  string
	ActorRole   string
	StateBefore json.RawMessage
	StateAfter  json.RawMessage
	Metadata    json.RawMessage
	LoggedAt    time.Time
}

// ListAudit -- GET /tasks/:id/activity (IG-97), terurut TERBARU dulu,
// paginasi offset sederhana (feed task tunggal, volume kecil per task --
// beda dari audit trail workspace/grup yang butuh cursor/filter kompleks).
// UNION dengan entity_type='task_attachment' (S4W-20/EPIC 10, audit
// lampiran SUDAH ada sejak awal lewat insertAttachmentAudit) -- audit
// lampiran tidak menyimpan task_id di metadata-nya, jadi dicocokkan lewat
// JOIN task_attachments di sini, bukan menambah kolom baru ke audit_logs.
// Konsisten dengan teks kosong desain "PM Task Detail.dc.html": "Perubahan
// status, role, lampiran, dan dependency akan muncul di sini".
func (r *TaskRepository) ListAudit(ctx context.Context, exec db.Executor, taskID string, limit, offset int) ([]AuditEntry, int, error) {
	const unionSQL = `
		SELECT al.id, al.action, al.actor_id, al.actor_role, al.state_before, al.state_after, al.metadata, al.logged_at
		FROM audit_logs al
		WHERE al.entity_type = 'task' AND al.entity_id = $1
		UNION ALL
		SELECT al.id, al.action, al.actor_id, al.actor_role, al.state_before, al.state_after, al.metadata, al.logged_at
		FROM audit_logs al
		JOIN task_attachments ta ON ta.id = al.entity_id
		WHERE al.entity_type = 'task_attachment' AND ta.task_id = $1
	`
	var total int
	if err := exec.QueryRow(ctx, `SELECT COUNT(*) FROM (`+unionSQL+`) x`, taskID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.ListAudit: count: %w", err)
	}
	rows, err := exec.Query(ctx, `
		SELECT x.id, x.action, x.actor_id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		       x.actor_role, x.state_before, x.state_after, x.metadata, x.logged_at
		FROM (`+unionSQL+`) x
		LEFT JOIN users u ON u.id = x.actor_id
		ORDER BY x.logged_at DESC
		LIMIT $2 OFFSET $3
	`, taskID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.ListAudit: %w", err)
	}
	defer rows.Close()

	list := make([]AuditEntry, 0)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Action, &e.ActorID, &e.ActorName, &e.ActorEmail, &e.ActorRole, &e.StateBefore, &e.StateAfter, &e.Metadata, &e.LoggedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.ListAudit: scan: %w", err)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.ListAudit: %w", err)
	}
	return list, total, nil
}

// TaskVersionSnapshot -- satu baris RIWAYAT VERSI (IG-97), tabel
// `task_version_snapshots` (DATABASE_SCHEMA.md §5.19 -- sebelumnya
// terdokumentasi tapi TIDAK PERNAH dimigrasikan/dipakai kode apa pun,
// "tabel hantu"). `Trigger` kolom TAMBAHAN (tidak ada di dokumentasi §5.19
// asli) -- desain "PM Task Detail.dc.html" minta alasan singkat per versi
// ("Deskripsi diubah"/"Judul diubah"); field `files`/`hasFiles` di desain
// SENGAJA tidak diikutkan -- itu cuma duplikat daftar lampiran task yang
// sudah ada di tab LAMPIRAN sendiri, snapshot per-versi tidak py makna
// tambahan untuk file (lampiran tidak versioned, cuma deskripsi/judul).
type TaskVersionSnapshot struct {
	ID           string
	TaskID       string
	Title        string
	Description  json.RawMessage
	ChangedBy    *string
	ChangedName  string
	ChangedEmail string
	Trigger      string
	SnapshotAt   time.Time
}

// CreateVersionSnapshot -- dipanggil SEBELUM tasks.title/description
// disimpan (TaskService.Update), sama urutan yang didokumentasikan
// DATABASE_SCHEMA.md §5.19: "Snapshot diambil SEBELUM perubahan disimpan".
func (r *TaskRepository) CreateVersionSnapshot(ctx context.Context, exec db.Executor, taskID, title string, description json.RawMessage, changedBy, trigger string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO task_version_snapshots (task_id, title, description, changed_by, trigger)
		VALUES ($1, $2, $3, $4, $5)
	`, taskID, title, description, changedBy, trigger)
	if err != nil {
		return fmt.Errorf("repository.CreateVersionSnapshot: %w", err)
	}
	return nil
}

// ListVersionSnapshots -- terurut TERBARU dulu (v1 = read-only history,
// tanpa restore/diff -- sesuai catatan §5.19 "dipertimbangkan untuk v2").
func (r *TaskRepository) ListVersionSnapshots(ctx context.Context, exec db.Executor, taskID string) ([]TaskVersionSnapshot, error) {
	rows, err := exec.Query(ctx, `
		SELECT tvs.id, tvs.task_id, tvs.title, tvs.description, tvs.changed_by,
		       COALESCE(u.display_name, ''), COALESCE(u.email, ''), tvs.trigger, tvs.snapshot_at
		FROM task_version_snapshots tvs
		LEFT JOIN users u ON u.id = tvs.changed_by
		WHERE tvs.task_id = $1
		ORDER BY tvs.snapshot_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListVersionSnapshots: %w", err)
	}
	defer rows.Close()

	list := make([]TaskVersionSnapshot, 0)
	for rows.Next() {
		var v TaskVersionSnapshot
		if err := rows.Scan(&v.ID, &v.TaskID, &v.Title, &v.Description, &v.ChangedBy, &v.ChangedName, &v.ChangedEmail, &v.Trigger, &v.SnapshotAt); err != nil {
			return nil, fmt.Errorf("repository.ListVersionSnapshots: scan: %w", err)
		}
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListVersionSnapshots: %w", err)
	}
	return list, nil
}

// ListDueForWorkspace (S4W-11, job harian due-date) -- task di seluruh
// project workspace ini dengan due_date dalam N hari ke depan (belum lewat,
// belum DONE). Assignee di-load agregat sama pola List (bukan N+1).
func (r *TaskRepository) ListDueForWorkspace(ctx context.Context, exec db.Executor, workspaceID string, days int) ([]Task, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		JOIN projects p ON p.id = t.project_id
		WHERE p.workspace_id = $1 AND t.deleted_at IS NULL AND t.due_date IS NOT NULL
		  AND t.due_date >= CURRENT_DATE AND t.due_date <= CURRENT_DATE + make_interval(days => $2)
		  AND cs.name != 'DONE'
	`, workspaceID, days)
	if err != nil {
		return nil, fmt.Errorf("repository.ListDueForWorkspace: %w", err)
	}
	defer rows.Close()

	list := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListDueForWorkspace: scan: %w", err)
		}
		list = append(list, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListDueForWorkspace: %w", err)
	}

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
			return nil, fmt.Errorf("repository.ListDueForWorkspace: assignees: %w", err)
		}
		defer aRows.Close()
		for aRows.Next() {
			var taskID string
			var a TaskAssignee
			if err := aRows.Scan(&taskID, &a.UserID, &a.DisplayName, &a.Email, &a.Role); err != nil {
				return nil, fmt.Errorf("repository.ListDueForWorkspace: assignees scan: %w", err)
			}
			if t, ok := byID[taskID]; ok {
				t.Assignees = append(t.Assignees, a)
			}
		}
	}
	return list, nil
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
