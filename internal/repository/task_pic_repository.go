// Package repository -- TaskPicRepository (Task Management Core Phase 2,
// US-017/017b: Phase PIC Handoff + PIC Group). Dua tabel terkait erat
// (task_pic_phases, pic_group_configs) digabung satu file, pola sama
// WebhookRepository (webhook_configs+webhook_deliveries).
package repository

import (
	"context"
	"fmt"
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
	return tag.RowsAffected() > 0, nil
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

func (r *TaskPicRepository) AddGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID, addedBy string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO pic_group_configs (project_id, status_id, user_id, added_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, status_id, user_id) DO NOTHING
	`, projectID, statusID, userID, addedBy)
	if err != nil {
		return fmt.Errorf("repository.AddGroupMember: %w", err)
	}
	return nil
}

func (r *TaskPicRepository) RemoveGroupMember(ctx context.Context, exec db.Executor, projectID, statusID, userID string) error {
	_, err := exec.Exec(ctx, `DELETE FROM pic_group_configs WHERE project_id = $1 AND status_id = $2 AND user_id = $3`, projectID, statusID, userID)
	if err != nil {
		return fmt.Errorf("repository.RemoveGroupMember: %w", err)
	}
	return nil
}
