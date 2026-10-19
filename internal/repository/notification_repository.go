package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// NotificationRepository -- lonceng notifikasi in-app (US-034, S6E-14, IG-145).
//
// KEAMANAN: RLS notifications_select_own mengizinkan Platform Admin membaca SEMUA baris (jalur job trusted,
// IG-39), jadi SETIAP query di sini memakai `n.user_id = $1` eksplisit -- jangan pernah mengandalkan RLS.
type NotificationRepository struct{}

func NewNotificationRepository() *NotificationRepository { return &NotificationRepository{} }

// NotificationWindowDays -- notifikasi lebih lama dari ini tidak tampil di panel (AC US-034).
const NotificationWindowDays = 30

// NotificationRow -- satu notifikasi beserta tujuan tautannya.
type NotificationRow struct {
	ID          string
	Type        string
	ActorName   *string
	Title       string
	Body        *string
	EntityType  *string
	EntityID    *string
	ProjectID   *string
	ProjectName *string
	TaskCode    *string
	IsRead      bool
	CreatedAt   time.Time
}

// notificationFrom -- FROM + WHERE bersama (daftar, hitung, tandai semua) supaya filter tidak pernah berselisih.
// Tujuan tautan diturunkan: notifikasi `task` menunjuk task-nya; `time_entry` hanya membawa id entri sehingga
// diselesaikan ke task induknya; `project` menunjuk project. Project yang tidak terlihat (RLS) atau jenis lain
// (keamanan/akun) dianggap tanpa project -> ikut tampil di workspace mana pun. $1 = user, $2 = workspace (kosong = semua).
const notificationFrom = `
	FROM notifications n
	LEFT JOIN users u ON u.id = n.actor_id
	LEFT JOIN time_entries te ON n.entity_type = 'time_entry' AND te.id = n.entity_id
	LEFT JOIN tasks rt ON rt.id = CASE n.entity_type WHEN 'task' THEN n.entity_id WHEN 'time_entry' THEN te.task_id END
	LEFT JOIN projects p ON p.id = COALESCE(rt.project_id, CASE n.entity_type WHEN 'project' THEN n.entity_id END)
	WHERE n.user_id = $1
	  AND n.created_at > NOW() - INTERVAL '30 days'
	  AND ($2 = '' OR p.id IS NULL OR p.workspace_id::text = $2)`

// List -- satu halaman (terbaru dulu) + total di jendela 30 hari.
func (r *NotificationRepository) List(ctx context.Context, exec db.Executor, userID, workspaceID string, limit, offset int) ([]NotificationRow, int, error) {
	var total int
	if err := exec.QueryRow(ctx, `SELECT COUNT(*) `+notificationFrom, userID, workspaceID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.NotificationList: count: %w", err)
	}
	rows, err := exec.Query(ctx, `
		SELECT n.id, n.type, u.display_name, COALESCE(n.title, ''), n.body,
		       CASE WHEN n.entity_type = 'time_entry' AND rt.id IS NOT NULL THEN 'task' ELSE n.entity_type END,
		       CASE WHEN n.entity_type = 'time_entry' THEN rt.id::text ELSE n.entity_id::text END,
		       p.id::text, p.name, rt.task_code, n.is_read, n.created_at
		`+notificationFrom+`
		ORDER BY n.created_at DESC, n.id
		LIMIT $3 OFFSET $4
	`, userID, workspaceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.NotificationList: %w", err)
	}
	defer rows.Close()
	list := make([]NotificationRow, 0)
	for rows.Next() {
		var n NotificationRow
		if err := rows.Scan(&n.ID, &n.Type, &n.ActorName, &n.Title, &n.Body, &n.EntityType, &n.EntityID, &n.ProjectID, &n.ProjectName, &n.TaskCode, &n.IsRead, &n.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.NotificationList: scan: %w", err)
		}
		list = append(list, n)
	}
	return list, total, rows.Err()
}

// UnreadCount -- jumlah belum dibaca pada cakupan yang sama dengan List.
func (r *NotificationRepository) UnreadCount(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error) {
	var n int
	if err := exec.QueryRow(ctx, `SELECT COUNT(*) `+notificationFrom+` AND NOT n.is_read`, userID, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository.NotificationUnreadCount: %w", err)
	}
	return n, nil
}

// MarkRead -- idempoten; id milik orang lain atau tidak ada -> ErrNotificationNotFound.
func (r *NotificationRepository) MarkRead(ctx context.Context, exec db.Executor, userID, id string) error {
	tag, err := exec.Exec(ctx, `UPDATE notifications SET is_read = TRUE WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("repository.NotificationMarkRead: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.NotificationMarkRead: %w", domain.ErrNotificationNotFound)
	}
	return nil
}

// MarkAllRead -- menandai seluruh yang belum dibaca pada cakupan (workspace) yang sama dengan List; mengembalikan jumlahnya.
func (r *NotificationRepository) MarkAllRead(ctx context.Context, exec db.Executor, userID, workspaceID string) (int, error) {
	tag, err := exec.Exec(ctx, `
		UPDATE notifications SET is_read = TRUE
		WHERE user_id = $1 AND id IN (SELECT n.id `+notificationFrom+` AND NOT n.is_read)
	`, userID, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("repository.NotificationMarkAllRead: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
