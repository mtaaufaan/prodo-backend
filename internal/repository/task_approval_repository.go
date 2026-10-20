package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// TaskApprovalRepository -- keputusan approval task (S7A-04, Sprint 7 "Approver Console", IG-150):
// antrean GET /me/approvals, riwayat keputusan permanen (task_approvals), kunci baris untuk keputusan
// serentak, dan jalur ubah status khusus keputusan (audit task.approved / task.rejected).
type TaskApprovalRepository struct{}

func NewTaskApprovalRepository() *TaskApprovalRepository { return &TaskApprovalRepository{} }

const (
	ApprovalViewPending = "pending"
	ApprovalViewDecided = "decided"
)

// ApprovalRow -- satu baris antrean (pending) atau riwayat (decided). Pada riwayat, bidang task adalah keadaan
// task SAAT INI; keputusan/waktu/catatan milik keputusan itu. ApprovalID kosong pada baris pending.
type ApprovalRow struct {
	ApprovalID    *string
	TaskID        string
	TaskCode      string
	Title         string
	ProjectID     string
	StatusName    string
	StatusColor   *string
	Priority      string
	SprintName    *string
	DueDate       *time.Time
	PicNames      []string
	CommentCount  int
	SubTotal      int
	SubDone       int
	LoggedMinutes int
	EstimateHours float64
	Decision      *string
	DecidedAt     *time.Time
	DecisionNote  *string
}

// ApprovalStats -- 4 kartu: pending = task UNDER REVIEW project; decided = total keputusan SAYA di project;
// total_tasks dan late menghitung SEMUA task project (desain "TM Antrean Approval").
type ApprovalStats struct {
	Pending    int
	Decided    int
	TotalTasks int
	Late       int
}

// LockStatus -- kunci baris task (FOR UPDATE) lalu kembalikan nama status dan project-nya. Dua keputusan serentak
// pada task yang sama diserialkan di sini: yang kedua membaca status SETELAH keputusan pertama.
func (r *TaskApprovalRepository) LockStatus(ctx context.Context, exec db.Executor, taskID string) (statusName, projectID string, err error) {
	err = exec.QueryRow(ctx, `
		SELECT cs.name, t.project_id
		FROM tasks t JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.id = $1 AND t.deleted_at IS NULL
		FOR UPDATE OF t
	`, taskID).Scan(&statusName, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("repository.LockStatus: %w", domain.ErrTaskNotFound)
	}
	if err != nil {
		return "", "", fmt.Errorf("repository.LockStatus: %w", err)
	}
	return statusName, projectID, nil
}

// StatusByName -- status project menurut nama (DONE, IN PROGRESS, ...), hanya yang belum dihapus (undefined).
func (r *TaskApprovalRepository) StatusByName(ctx context.Context, exec db.Executor, projectID, name string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `
		SELECT `+customStatusSelectColumns+`
		FROM custom_statuses
		WHERE scope_type = 'project' AND scope_id = $1 AND name = $2 AND NOT is_undefined
	`, projectID, name)
	s, err := scanCustomStatus(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.StatusByName: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.StatusByName: %w", err)
	}
	return s, nil
}

// LastPhasePics -- PIC fase `statusID` pada kesempatan terakhir fase itu berakhir: baris dengan deactivated_at
// terbaru (semua PIC aktif dinonaktifkan serentak saat status berpindah, jadi stempelnya identik; PIC yang
// dihapus lebih awal punya stempel lebih lama dan tidak ikut).
func (r *TaskApprovalRepository) LastPhasePics(ctx context.Context, exec db.Executor, taskID, statusID string) ([]string, error) {
	rows, err := exec.Query(ctx, `
		SELECT user_id::text FROM task_pic_phases
		WHERE task_id = $1 AND status_id = $2 AND deactivated_at IS NOT NULL
		  AND deactivated_at = (SELECT MAX(deactivated_at) FROM task_pic_phases WHERE task_id = $1 AND status_id = $2)
		ORDER BY activated_at, user_id
	`, taskID, statusID)
	if err != nil {
		return nil, fmt.Errorf("repository.LastPhasePics: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repository.LastPhasePics: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Recipients -- assignee + PIC aktif task (tanpa duplikat dan tanpa `exceptUserID`, yaitu pengambil keputusan).
// Dipanggil SEBELUM status berpindah supaya PIC fase review masih aktif.
func (r *TaskApprovalRepository) Recipients(ctx context.Context, exec db.Executor, taskID, exceptUserID string) ([]string, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id::text FROM (
			SELECT user_id FROM task_assignees WHERE task_id = $1
			UNION
			SELECT user_id FROM task_pic_phases WHERE task_id = $1 AND is_active
		) x JOIN users u ON u.id = x.user_id
		WHERE u.id <> $2::uuid
	`, taskID, exceptUserID)
	if err != nil {
		return nil, fmt.Errorf("repository.Recipients: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repository.Recipients: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetStatusByApproval -- ubah status karena keputusan approval: sama dengan TaskRepository.SetStatus (completed_at
// bila DONE) tetapi audit-nya `task.approved` / `task.rejected` (state_before/after status + catatan), bukan
// `task.status_changed`, supaya satu keputusan = satu baris audit.
func (r *TaskApprovalRepository) SetStatusByApproval(ctx context.Context, exec db.Executor, taskID, statusID string, isDone bool, actorID, actorRole, workspaceID, action, statusBefore, statusAfter, note string) error {
	var completedAt any
	if isDone {
		completedAt = time.Now()
	}
	tag, err := exec.Exec(ctx, `
		UPDATE tasks SET status_id = $2, completed_at = $3, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID, statusID, completedAt)
	if err != nil {
		return fmt.Errorf("repository.SetStatusByApproval: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetStatusByApproval: %w", domain.ErrTaskNotFound)
	}
	after := map[string]any{"status": statusAfter}
	if strings.TrimSpace(note) != "" {
		after["note"] = note
	}
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, action, taskID, workspaceID, map[string]any{"status": statusBefore}, after); err != nil {
		return fmt.Errorf("repository.SetStatusByApproval: audit: %w", err)
	}
	return nil
}

// Record -- simpan keputusan (append-only). Mengembalikan id keputusan.
func (r *TaskApprovalRepository) Record(ctx context.Context, exec db.Executor, taskID, approverID, decision, note, statusFrom, statusTo string) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO task_approvals (task_id, approver_id, decision, note, status_from, status_to)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)
		RETURNING id::text
	`, taskID, approverID, decision, note, statusFrom, statusTo).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.Record: %w", err)
	}
	return id, nil
}

// NotifyDecision -- notifikasi in-app `task_approved` / `task_rejected` ke `recipientIDs`; alasan tolak ikut di isi.
func (r *TaskApprovalRepository) NotifyDecision(ctx context.Context, exec db.Executor, taskID, actorID string, recipientIDs []string, approved bool, note string) error {
	if len(recipientIDs) == 0 {
		return nil
	}
	typ := "task_rejected"
	title := `'Task ditolak -- ' || COALESCE(t.task_code, '')`
	body := `'Task "' || t.title || '" ditolak. Alasan: ' || $4`
	if approved {
		typ = "task_approved"
		title = `'Task disetujui -- ' || COALESCE(t.task_code, '')`
		body = `'Task "' || t.title || '" disetujui.' || CASE WHEN $4 = '' THEN '' ELSE ' Catatan: ' || $4 END`
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
		SELECT rid, $3::uuid, '`+typ+`', 'task', t.id, `+title+`, `+body+`
		FROM tasks t, unnest($2::uuid[]) AS rid
		WHERE t.id = $1
	`, taskID, recipientIDs, actorID, note)
	if err != nil {
		return fmt.Errorf("repository.NotifyDecision: %w", err)
	}
	return nil
}

// Follows -- apakah userID "mengikuti" project (anggota project, assignee, atau PIC aktif); dipakai gerbang Approver.
func (r *TaskApprovalRepository) Follows(ctx context.Context, exec db.Executor, projectID, userID string) (bool, error) {
	var ok bool
	err := exec.QueryRow(ctx, `SELECT `+followedPredicate+` FROM projects p WHERE p.id = $2`, userID, projectID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("repository.Follows: %w", err)
	}
	return ok, nil
}

const approvalLateSQL = `(t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE AND cs.name NOT IN ('DONE', 'CANCELED'))`

// approvalCardColumns -- bidang kartu task (sama pola GET /me/tasks).
const approvalCardColumns = `
	t.id::text, COALESCE(t.task_code, ''), t.title, t.project_id::text, cs.name, cs.color_token, t.priority::text,
	s.name, t.due_date,
	COALESCE((SELECT array_agg(u.display_name ORDER BY u.display_name) FROM task_pic_phases tpp
	          JOIN users u ON u.id = tpp.user_id WHERE tpp.task_id = t.id AND tpp.is_active), '{}'::text[]),
	(SELECT COUNT(*) FROM task_comments c WHERE c.task_id = t.id AND c.deleted_at IS NULL)::int,
	(SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id)::int,
	(SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id AND ci.is_done)::int,
	COALESCE((SELECT SUM(te.duration_minutes) FROM time_entries te WHERE te.task_id = t.id AND te.is_approved = TRUE), 0)::int,
	COALESCE(t.estimated_hours, 0)::float8`

// List -- satu halaman antrean (pending: task UNDER REVIEW project) atau riwayat keputusan userID (decided,
// terbaru dulu, satu baris per keputusan); total = seluruh baris view itu.
func (r *TaskApprovalRepository) List(ctx context.Context, exec db.Executor, projectID, userID, view string, limit, offset int) ([]ApprovalRow, int, error) {
	var total int
	var query string
	args := []any{projectID, limit, offset}
	if view == ApprovalViewDecided {
		args = []any{projectID, userID, limit, offset}
		if err := exec.QueryRow(ctx, `
			SELECT COUNT(*) FROM task_approvals a JOIN tasks t ON t.id = a.task_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND a.approver_id = $2`, projectID, userID).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("repository.Approvals count: %w", err)
		}
		query = `
			SELECT ` + approvalCardColumns + `, a.id::text, a.decision, a.decided_at, a.note
			FROM task_approvals a
			JOIN tasks t ON t.id = a.task_id
			JOIN custom_statuses cs ON cs.id = t.status_id
			LEFT JOIN sprints s ON s.id = t.sprint_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND a.approver_id = $2
			ORDER BY a.decided_at DESC, a.id
			LIMIT $3 OFFSET $4`
	} else {
		if err := exec.QueryRow(ctx, `
			SELECT COUNT(*) FROM tasks t JOIN custom_statuses cs ON cs.id = t.status_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND cs.name = 'UNDER REVIEW'`, projectID).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("repository.Approvals count: %w", err)
		}
		query = `
			SELECT ` + approvalCardColumns + `, NULL::text, NULL::text, NULL::timestamptz, NULL::text
			FROM tasks t
			JOIN custom_statuses cs ON cs.id = t.status_id
			LEFT JOIN sprints s ON s.id = t.sprint_id
			WHERE t.project_id = $1 AND t.deleted_at IS NULL AND cs.name = 'UNDER REVIEW'
			ORDER BY t.due_date NULLS LAST, t.created_at, t.id
			LIMIT $2 OFFSET $3`
	}
	rows, err := exec.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.Approvals list: %w", err)
	}
	defer rows.Close()
	list := make([]ApprovalRow, 0)
	for rows.Next() {
		var a ApprovalRow
		if err := rows.Scan(&a.TaskID, &a.TaskCode, &a.Title, &a.ProjectID, &a.StatusName, &a.StatusColor, &a.Priority,
			&a.SprintName, &a.DueDate, &a.PicNames, &a.CommentCount, &a.SubTotal, &a.SubDone, &a.LoggedMinutes, &a.EstimateHours,
			&a.ApprovalID, &a.Decision, &a.DecidedAt, &a.DecisionNote); err != nil {
			return nil, 0, fmt.Errorf("repository.Approvals scan: %w", err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.Approvals rows: %w", err)
	}
	return list, total, nil
}

// Stats -- 4 kartu untuk project.
func (r *TaskApprovalRepository) Stats(ctx context.Context, exec db.Executor, projectID, userID string) (ApprovalStats, error) {
	var s ApprovalStats
	err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE cs.name = 'UNDER REVIEW')::int,
		       COUNT(*)::int,
		       COUNT(*) FILTER (WHERE `+approvalLateSQL+`)::int,
		       (SELECT COUNT(*) FROM task_approvals a JOIN tasks t2 ON t2.id = a.task_id
		        WHERE t2.project_id = $1 AND t2.deleted_at IS NULL AND a.approver_id = $2)::int
		FROM tasks t JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL
	`, projectID, userID).Scan(&s.Pending, &s.TotalTasks, &s.Late, &s.Decided)
	if err != nil {
		return s, fmt.Errorf("repository.Approvals stats: %w", err)
	}
	return s, nil
}
