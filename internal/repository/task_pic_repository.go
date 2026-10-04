// Package repository -- TaskPicRepository (Task Management Core Phase 2,
// US-017/017b: Phase PIC Handoff + PIC Group). Dua tabel terkait erat
// (task_pic_phases, pic_group_configs) digabung satu file, pola sama
// WebhookRepository (webhook_configs+webhook_deliveries).
package repository

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type TaskPicPhase struct {
	ID             string
	TaskID         string
	StatusID       string
	StatusName     string
	UserID         string
	UserName       string
	UserEmail      string
	IsActive       bool
	AcknowledgedAt *time.Time
	ActivatedAt    time.Time
	DeactivatedAt  *time.Time
	AssignedBy     *string
}

type PicGroupMember struct {
	ProjectID string
	StatusID  string
	UserID    string
	UserName  string
	UserEmail string
	AddedBy   *string
	CreatedAt time.Time
}

type TaskPicRepository struct{}

func NewTaskPicRepository() *TaskPicRepository {
	return &TaskPicRepository{}
}

// CreatePhase -- satu baris PER PIC per fase (S4-29/31/32), TIDAK
// diupdate di tempat -- riwayat lengkap tersimpan (§5.18 catatan).
func (r *TaskPicRepository) CreatePhase(ctx context.Context, exec db.Executor, taskID, statusID, userID string, assignedBy *string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO task_pic_phases (task_id, status_id, user_id, assigned_by)
		VALUES ($1, $2, $3, $4)
	`, taskID, statusID, userID, assignedBy)
	if err != nil {
		return fmt.Errorf("repository.CreatePhase: %w", err)
	}
	return nil
}

// DeactivateActiveForTask -- dipanggil SEBELUM membuat fase baru saat
// status berganti (S4-31: "old PIC inactive -> new PIC pending").
func (r *TaskPicRepository) DeactivateActiveForTask(ctx context.Context, exec db.Executor, taskID string) error {
	_, err := exec.Exec(ctx, `
		UPDATE task_pic_phases SET is_active = FALSE, deactivated_at = NOW()
		WHERE task_id = $1 AND is_active = TRUE
	`, taskID)
	if err != nil {
		return fmt.Errorf("repository.DeactivateActiveForTask: %w", err)
	}
	return nil
}

const picPhaseSelectColumns = `
	tpp.id, tpp.task_id, tpp.status_id, cs.name, tpp.user_id, u.display_name, u.email,
	tpp.is_active, tpp.acknowledged_at, tpp.activated_at, tpp.deactivated_at, tpp.assigned_by
`

func scanPicPhase(row interface{ Scan(dest ...any) error }) (*TaskPicPhase, error) {
	var p TaskPicPhase
	if err := row.Scan(&p.ID, &p.TaskID, &p.StatusID, &p.StatusName, &p.UserID, &p.UserName, &p.UserEmail,
		&p.IsActive, &p.AcknowledgedAt, &p.ActivatedAt, &p.DeactivatedAt, &p.AssignedBy); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListActiveForTask -- PIC aktif saat ini (§5.18: "WHERE task_id = $1 AND
// is_active = TRUE"), bisa lebih dari satu (fase bisa punya beberapa PIC
// sekaligus, desain "PILIH PIC FASE" mengizinkan multi-pilih).
func (r *TaskPicRepository) ListActiveForTask(ctx context.Context, exec db.Executor, taskID string) ([]TaskPicPhase, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+picPhaseSelectColumns+`
		FROM task_pic_phases tpp
		JOIN custom_statuses cs ON cs.id = tpp.status_id
		JOIN users u ON u.id = tpp.user_id
		WHERE tpp.task_id = $1 AND tpp.is_active = TRUE
		ORDER BY tpp.activated_at
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListActiveForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TaskPicPhase, 0)
	for rows.Next() {
		p, err := scanPicPhase(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListActiveForTask: scan: %w", err)
		}
		list = append(list, *p)
	}
	return list, rows.Err()
}

// ListHistoryForTask -- seluruh fase PIC (FE PICHistoryTab), terbaru dulu.
func (r *TaskPicRepository) ListHistoryForTask(ctx context.Context, exec db.Executor, taskID string) ([]TaskPicPhase, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+picPhaseSelectColumns+`
		FROM task_pic_phases tpp
		JOIN custom_statuses cs ON cs.id = tpp.status_id
		JOIN users u ON u.id = tpp.user_id
		WHERE tpp.task_id = $1
		ORDER BY tpp.activated_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListHistoryForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TaskPicPhase, 0)
	for rows.Next() {
		p, err := scanPicPhase(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListHistoryForTask: scan: %w", err)
		}
		list = append(list, *p)
	}
	return list, rows.Err()
}

// IsActivePic -- dipakai guard PUT /tasks/:id/completeness (Phase 3,
// S4-44: "hanya pembuat task dan PIC aktif").
func (r *TaskPicRepository) IsActivePic(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM task_pic_phases WHERE task_id = $1 AND user_id = $2 AND is_active = TRUE)
	`, taskID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.IsActivePic: %w", err)
	}
	return exists, nil
}

// Acknowledge -- S4-33, PIC aktif konfirmasi menerima serah terima.
// tag.RowsAffected()==0 berarti actor bukan PIC aktif task ini (atau sudah
// acknowledge sebelumnya) -- pemanggil (service) menerjemahkan ke error.
func (r *TaskPicRepository) Acknowledge(ctx context.Context, exec db.Executor, taskID, userID string) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE task_pic_phases SET acknowledged_at = NOW()
		WHERE task_id = $1 AND user_id = $2 AND is_active = TRUE AND acknowledged_at IS NULL
	`, taskID, userID)
	if err != nil {
		return false, fmt.Errorf("repository.Acknowledge: %w", err)
	}
	ok := tag.RowsAffected() > 0
	if ok {
		// workspaceID via subquery langsung (IG-94) -- Acknowledge tidak py
		// akses projectID/workspaceID Go-level (cuma taskID/userID), lebih
		// murah 1 query SQL daripada nambah dependency taskProjectResolver
		// ke TaskPicService cuma untuk audit satu aksi kecil ini.
		var workspaceID string
		if err := exec.QueryRow(ctx, `SELECT p.workspace_id FROM tasks t JOIN projects p ON p.id = t.project_id WHERE t.id = $1`, taskID).Scan(&workspaceID); err != nil {
			return false, fmt.Errorf("repository.Acknowledge: workspace: %w", err)
		}
		if err := insertTaskAudit(ctx, exec, userID, "", "task.pic_acknowledged", taskID, workspaceID, nil, nil); err != nil {
			return false, fmt.Errorf("repository.Acknowledge: audit: %w", err)
		}
	}
	return ok, nil
}

// AddPic (IG-97 susulan, tab PIC FASE "+ Tambah PIC Paralel") -- PIC
// tambahan pada fase (status) yang SEDANG aktif, TANPA menonaktifkan PIC
// lain -- beda dari CreatePhase yang dipakai SetStatus (satu fase baru
// menggantikan seluruhnya). Audit "task.pic_added" SENDIRI di sini --
// CreatePhase tidak audit sendiri karena selalu menyertai audit
// "task.status_changed" tunggal yang sudah mencakupnya.
func (r *TaskPicRepository) AddPic(ctx context.Context, exec db.Executor, taskID, statusID, statusName, userID, actorID, actorRole, workspaceID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO task_pic_phases (task_id, status_id, user_id, assigned_by)
		VALUES ($1, $2, $3, $4)
	`, taskID, statusID, userID, actorID)
	if err != nil {
		return fmt.Errorf("repository.AddPic: %w", err)
	}
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.pic_added", taskID, workspaceID, nil,
		map[string]any{"status_name": statusName, "user_id": userID}); err != nil {
		return fmt.Errorf("repository.AddPic: audit: %w", err)
	}
	return nil
}

// RemovePic (IG-97 susulan, tombol "✕ HAPUS PIC") -- lepas SATU PIC
// aktif. Guard "bukan PIC terakhir" ada di service SEBELUM memanggil ini
// (lewat ListActiveForTask) -- repo ini murni eksekusi + audit.
func (r *TaskPicRepository) RemovePic(ctx context.Context, exec db.Executor, taskID, statusName, userID, actorID, actorRole, workspaceID string) (bool, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE task_pic_phases SET is_active = FALSE, deactivated_at = NOW()
		WHERE task_id = $1 AND user_id = $2 AND is_active = TRUE
	`, taskID, userID)
	if err != nil {
		return false, fmt.Errorf("repository.RemovePic: %w", err)
	}
	ok := tag.RowsAffected() > 0
	if ok {
		if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.pic_removed", taskID, workspaceID, nil,
			map[string]any{"status_name": statusName, "user_id": userID}); err != nil {
			return false, fmt.Errorf("repository.RemovePic: audit: %w", err)
		}
	}
	return ok, nil
}

// deactivateOnePic -- lepas satu PIC aktif TANPA audit sendiri -- dipakai
// HandoffPic sebagai langkah "PIC lama dilepas" di dalam SATU aksi serah
// terima (audit cukup satu baris "task.pic_handoff" yang mencakup dari+ke,
// bukan baris terpisah per PIC yang dilepas).
func (r *TaskPicRepository) deactivateOnePic(ctx context.Context, exec db.Executor, taskID, userID string) error {
	_, err := exec.Exec(ctx, `
		UPDATE task_pic_phases SET is_active = FALSE, deactivated_at = NOW()
		WHERE task_id = $1 AND user_id = $2 AND is_active = TRUE
	`, taskID, userID)
	if err != nil {
		return fmt.Errorf("repository.deactivateOnePic: %w", err)
	}
	return nil
}

// HandoffPic (IG-97 susulan, "SERAHKAN PIC FASE") -- lepas PIC lama
// (fromUserIDs -- satu user tertentu, atau SEMUA PIC aktif kalau dipanggil
// dengan seluruh isi ListActiveForTask), lalu buat fase baru (PENDING)
// untuk toUserID. Satu audit "task.pic_handoff" mencakup keduanya --
// BUKAN "task.pic_removed"+"task.pic_added" terpisah, supaya feed
// AKTIVITAS/Audit Trail membaca ini sebagai satu peristiwa serah terima.
func (r *TaskPicRepository) HandoffPic(ctx context.Context, exec db.Executor, taskID, statusID, statusName string, fromUserIDs []string, toUserID, actorID, actorRole, workspaceID string) error {
	for _, uid := range fromUserIDs {
		if err := r.deactivateOnePic(ctx, exec, taskID, uid); err != nil {
			return fmt.Errorf("repository.HandoffPic: %w", err)
		}
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO task_pic_phases (task_id, status_id, user_id, assigned_by)
		VALUES ($1, $2, $3, $4)
	`, taskID, statusID, toUserID, actorID)
	if err != nil {
		return fmt.Errorf("repository.HandoffPic: %w", err)
	}
	if err := insertTaskAudit(ctx, exec, actorID, actorRole, "task.pic_handoff", taskID, workspaceID, nil,
		map[string]any{"status_name": statusName, "from_user_ids": fromUserIDs, "to_user_id": toUserID}); err != nil {
		return fmt.Errorf("repository.HandoffPic: audit: %w", err)
	}
	return nil
}

// ListGroupForStatus -- PIC Group (project_id, status_id) tertentu --
// kosong berarti mode Bebas (§5.34).
func (r *TaskPicRepository) ListGroupForStatus(ctx context.Context, exec db.Executor, projectID, statusID string) ([]PicGroupMember, error) {
	rows, err := exec.Query(ctx, `
		SELECT pgc.project_id, pgc.status_id, pgc.user_id, u.display_name, u.email, pgc.added_by, pgc.created_at
		FROM pic_group_configs pgc
		JOIN users u ON u.id = pgc.user_id
		WHERE pgc.project_id = $1 AND pgc.status_id = $2
	`, projectID, statusID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListGroupForStatus: %w", err)
	}
	defer rows.Close()
	return scanPicGroupMembers(rows)
}

// ListGroupForProject -- seluruh konfigurasi PIC Group project (semua
// status sekaligus), dipakai halaman pengaturan.
func (r *TaskPicRepository) ListGroupForProject(ctx context.Context, exec db.Executor, projectID string) ([]PicGroupMember, error) {
	rows, err := exec.Query(ctx, `
		SELECT pgc.project_id, pgc.status_id, pgc.user_id, u.display_name, u.email, pgc.added_by, pgc.created_at
		FROM pic_group_configs pgc
		JOIN users u ON u.id = pgc.user_id
		WHERE pgc.project_id = $1
		ORDER BY pgc.status_id
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListGroupForProject: %w", err)
	}
	defer rows.Close()
	return scanPicGroupMembers(rows)
}

func scanPicGroupMembers(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]PicGroupMember, error) {
	list := make([]PicGroupMember, 0)
	for rows.Next() {
		var m PicGroupMember
		if err := rows.Scan(&m.ProjectID, &m.StatusID, &m.UserID, &m.UserName, &m.UserEmail, &m.AddedBy, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanPicGroupMembers: %w", err)
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// ReplaceGroup mengganti SELURUH anggota PIC Group (projectID, statusID)
// dengan userIDs -- kosong = Full handoff (tidak ada pembatasan). Satu
// operasi atomik (transaksi request) dan SATU entri audit "pic_group.updated"
// dengan state_before/state_after berisi nama anggota (snapshot, bukan
// JOIN langsung -- pola audit trail IG-26/IG-29: nama user bisa berubah/
// dihapus kemudian). Tidak ada perubahan -> tidak ada entri audit.
// workspaceID menjadikan baris terlihat di Audit Trail Workspace.
func (r *TaskPicRepository) ReplaceGroup(ctx context.Context, exec db.Executor, projectID, statusID string, userIDs []string, actorID, actorRole, workspaceID string) ([]PicGroupMember, error) {
	before, err := r.ListGroupForStatus(ctx, exec, projectID, statusID)
	if err != nil {
		return nil, err
	}
	if _, err := exec.Exec(ctx, `DELETE FROM pic_group_configs WHERE project_id = $1 AND status_id = $2`, projectID, statusID); err != nil {
		return nil, fmt.Errorf("repository.ReplaceGroup: delete: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := exec.Exec(ctx, `
			INSERT INTO pic_group_configs (project_id, status_id, user_id, added_by)
			VALUES ($1, $2, $3, $4)
		`, projectID, statusID, userID, actorID); err != nil {
			return nil, fmt.Errorf("repository.ReplaceGroup: insert: %w", err)
		}
	}
	after, err := r.ListGroupForStatus(ctx, exec, projectID, statusID)
	if err != nil {
		return nil, err
	}
	if sameGroupMembers(before, after) {
		return after, nil
	}

	var projectName, statusName string
	if err := exec.QueryRow(ctx, `
		SELECT p.name, cs.name FROM projects p, custom_statuses cs WHERE p.id = $1 AND cs.id = $2
	`, projectID, statusID).Scan(&projectName, &statusName); err != nil {
		return nil, fmt.Errorf("repository.ReplaceGroup: snapshot nama: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "pic_group.updated", projectID, workspaceID,
		map[string]any{"members": picGroupNames(before)}, map[string]any{"members": picGroupNames(after)},
		map[string]any{"project_name": projectName, "status_id": statusID, "status_name": statusName}); err != nil {
		return nil, fmt.Errorf("repository.ReplaceGroup: audit: %w", err)
	}
	return after, nil
}

func sameGroupMembers(a, b []PicGroupMember) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, m := range a {
		seen[m.UserID] = true
	}
	for _, m := range b {
		if !seen[m.UserID] {
			return false
		}
	}
	return true
}

// picGroupNames -- nama tampil (fallback email), terurut supaya diff stabil.
func picGroupNames(list []PicGroupMember) []string {
	names := make([]string, 0, len(list))
	for _, m := range list {
		if m.UserName != "" {
			names = append(names, m.UserName)
		} else {
			names = append(names, m.UserEmail)
		}
	}
	sort.Strings(names)
	return names
}
