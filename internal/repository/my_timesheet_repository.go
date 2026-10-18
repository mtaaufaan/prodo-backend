package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// MyTimesheetRepository -- Timesheet Editor lintas project dalam satu workspace (S6E-10, IG-141; kontrak
// frontend S6E-09/IG-140). Jam yang dihitung = entri DISETUJUI milik user sendiri (RLS time_entries_select:
// non-PM hanya melihat entri miliknya); entri estimasi import (IG-129) ikut dengan flag is_estimate.
type MyTimesheetRepository struct{}

func NewMyTimesheetRepository() *MyTimesheetRepository { return &MyTimesheetRepository{} }

// MyHoursTask -- satu task beserta jam tercatat user ini.
type MyHoursTask struct {
	TaskID        string
	TaskCode      string
	Title         string
	ProjectID     string
	ProjectName   string
	StatusName    string
	EstimateHours *float64
	LoggedMinutes int
	WeekMinutes   int
	// IsMine -- user adalah assignee atau PIC aktif (task lain muncul hanya karena ada jam minggu ini).
	IsMine bool
}

// MyHoursEntry -- satu entri riwayat.
type MyHoursEntry struct {
	ID          string
	TaskID      string
	TaskCode    string
	TaskTitle   string
	ProjectName string
	EntryDate   string // YYYY-MM-DD (WIB)
	Minutes     int
	Note        string
	IsEstimate  bool
}

// ListTasks -- task milik user di workspace (assignee atau PIC aktif, belum dihapus) UNION task yang user catat
// jamnya pada minggu [weekStart, weekEnd) -- supaya panel minggu ini tetap lengkap setelah task diserahkan.
func (r *MyTimesheetRepository) ListTasks(ctx context.Context, exec db.Executor, userID, workspaceID string, weekStart, weekEnd time.Time) ([]MyHoursTask, error) {
	rows, err := exec.Query(ctx, `
		SELECT t.id, COALESCE(t.task_code, ''), t.title, p.id, p.name, cs.name, t.estimated_hours::float8,
		       COALESCE((SELECT SUM(te.duration_minutes) FROM time_entries te
		                 WHERE te.task_id = t.id AND te.user_id = $1 AND te.is_approved = TRUE), 0)::int,
		       COALESCE((SELECT SUM(te.duration_minutes) FROM time_entries te
		                 WHERE te.task_id = t.id AND te.user_id = $1 AND te.is_approved = TRUE
		                   AND te.started_at >= $3 AND te.started_at < $4), 0)::int,
		       (EXISTS (SELECT 1 FROM task_assignees a WHERE a.task_id = t.id AND a.user_id = $1)
		        OR EXISTS (SELECT 1 FROM task_pic_phases ph WHERE ph.task_id = t.id AND ph.user_id = $1 AND ph.is_active))
		FROM tasks t
		JOIN projects p ON p.id = t.project_id AND p.workspace_id = $2 AND p.deleted_at IS NULL
		JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.deleted_at IS NULL
		  AND (EXISTS (SELECT 1 FROM task_assignees a WHERE a.task_id = t.id AND a.user_id = $1)
		       OR EXISTS (SELECT 1 FROM task_pic_phases ph WHERE ph.task_id = t.id AND ph.user_id = $1 AND ph.is_active)
		       OR EXISTS (SELECT 1 FROM time_entries te WHERE te.task_id = t.id AND te.user_id = $1 AND te.is_approved = TRUE
		                  AND te.started_at >= $3 AND te.started_at < $4))
		ORDER BY t.created_at, t.task_code
	`, userID, workspaceID, weekStart, weekEnd)
	if err != nil {
		return nil, fmt.Errorf("repository.MyHoursListTasks: %w", err)
	}
	defer rows.Close()
	list := make([]MyHoursTask, 0)
	for rows.Next() {
		var t MyHoursTask
		if err := rows.Scan(&t.TaskID, &t.TaskCode, &t.Title, &t.ProjectID, &t.ProjectName, &t.StatusName, &t.EstimateHours, &t.LoggedMinutes, &t.WeekMinutes, &t.IsMine); err != nil {
			return nil, fmt.Errorf("repository.MyHoursListTasks: scan: %w", err)
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// ListEntries -- riwayat satu halaman (terbaru dulu) + total. Hanya entri yang sudah dihitung (disetujui,
// selesai); tanggal entri = tanggal mulai menurut WIB.
func (r *MyTimesheetRepository) ListEntries(ctx context.Context, exec db.Executor, userID, workspaceID string, limit, offset int) ([]MyHoursEntry, int, error) {
	var total int
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FROM time_entries te
		JOIN tasks t ON t.id = te.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE te.user_id = $1 AND p.workspace_id = $2 AND te.is_approved = TRUE AND te.duration_minutes IS NOT NULL
	`, userID, workspaceID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.MyHoursListEntries: count: %w", err)
	}
	rows, err := exec.Query(ctx, `
		SELECT te.id, te.task_id, COALESCE(t.task_code, ''), t.title, p.name,
		       to_char(te.started_at AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD'), te.duration_minutes, COALESCE(te.note, ''), te.is_estimate
		FROM time_entries te
		JOIN tasks t ON t.id = te.task_id
		JOIN projects p ON p.id = t.project_id
		WHERE te.user_id = $1 AND p.workspace_id = $2 AND te.is_approved = TRUE AND te.duration_minutes IS NOT NULL
		ORDER BY te.started_at DESC, te.created_at DESC
		LIMIT $3 OFFSET $4
	`, userID, workspaceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.MyHoursListEntries: %w", err)
	}
	defer rows.Close()
	list := make([]MyHoursEntry, 0)
	for rows.Next() {
		var e MyHoursEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.TaskCode, &e.TaskTitle, &e.ProjectName, &e.EntryDate, &e.Minutes, &e.Note, &e.IsEstimate); err != nil {
			return nil, 0, fmt.Errorf("repository.MyHoursListEntries: scan: %w", err)
		}
		list = append(list, e)
	}
	return list, total, rows.Err()
}
