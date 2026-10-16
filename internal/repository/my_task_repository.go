package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// MyTaskRepository -- GET /me/tasks (Task Saya Editor, S6E-04, IG-135; desain "TM Task Saya.dc.html").
type MyTaskRepository struct{}

func NewMyTaskRepository() *MyTaskRepository { return &MyTaskRepository{} }

const (
	MyTaskViewAssigned  = "assigned"
	MyTaskViewFollowing = "following"
)

// MyTaskNextStatus -- status berikutnya (menurut posisi) yang boleh dituju tombol "STATUS-BERIKUTNYA".
type MyTaskNextStatus struct {
	ID         string
	Name       string
	RequirePic bool
}

type MyTaskRow struct {
	TaskID        string
	TaskCode      string
	Title         string
	ProjectID     string
	StatusID      string
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
	IsMine        bool
	IsFollowed    bool
	IsLate        bool
	Next          *MyTaskNextStatus
}

// MyTaskStats -- 4 kartu: dihitung dari task "saya" (assignee atau PIC aktif), bukan dari tab.
type MyTaskStats struct {
	Total         int
	Running       int
	LoggedMinutes int
	Late          int
}

// mineSQL / followedSQL -- $2 = user id, alias tasks = t.
const (
	mineSQL     = `(EXISTS (SELECT 1 FROM task_assignees ta WHERE ta.task_id = t.id AND ta.user_id = $2) OR EXISTS (SELECT 1 FROM task_pic_phases tp WHERE tp.task_id = t.id AND tp.user_id = $2 AND tp.is_active))`
	followedSQL = `EXISTS (SELECT 1 FROM task_followers tf WHERE tf.task_id = t.id AND tf.user_id = $2)`
	// lateSQL -- lewat due date dan belum selesai/dibatalkan.
	lateSQL = `(t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE AND cs.name NOT IN ('DONE', 'CANCELED'))`
)

// List -- satu halaman task project untuk user, view assigned|following; total = jumlah seluruh
// baris view itu (untuk paginasi).
func (r *MyTaskRepository) List(ctx context.Context, exec db.Executor, projectID, userID, view string, limit, offset int) ([]MyTaskRow, int, error) {
	predicate := mineSQL
	if view == MyTaskViewFollowing {
		predicate = followedSQL
	}
	var total int
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FROM tasks t JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND `+predicate, projectID, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.MyTasks count: %w", err)
	}

	rows, err := exec.Query(ctx, `
		SELECT t.id, COALESCE(t.task_code, ''), t.title, t.project_id, t.status_id, cs.name, cs.color_token, t.priority::text,
		       s.name, t.due_date,
		       COALESCE((SELECT array_agg(u.display_name ORDER BY u.display_name) FROM task_pic_phases tpp
		                 JOIN users u ON u.id = tpp.user_id WHERE tpp.task_id = t.id AND tpp.is_active), '{}'::text[]),
		       (SELECT COUNT(*) FROM task_comments c WHERE c.task_id = t.id AND c.deleted_at IS NULL)::int,
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id)::int,
		       (SELECT COUNT(*) FROM task_checklist_items ci WHERE ci.task_id = t.id AND ci.is_done)::int,
		       COALESCE((SELECT SUM(te.duration_minutes) FROM time_entries te WHERE te.task_id = t.id AND te.is_approved = TRUE), 0)::int,
		       COALESCE(t.estimated_hours, 0)::float8,
		       `+mineSQL+`, `+followedSQL+`, `+lateSQL+`,
		       nxt.id::text, nxt.name, nxt.require_pic
		FROM tasks t
		JOIN custom_statuses cs ON cs.id = t.status_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		LEFT JOIN LATERAL (
			SELECT ns.id, ns.name, ns.require_pic FROM custom_statuses ns
			WHERE ns.scope_type = cs.scope_type AND ns.scope_id = cs.scope_id AND ns.position > cs.position
			  AND NOT ns.is_undefined AND ns.name NOT IN ('BLOCKED', 'CANCELED')
			ORDER BY ns.position LIMIT 1
		) nxt ON TRUE
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND `+predicate+`
		ORDER BY t.due_date NULLS LAST, t.created_at, t.id
		LIMIT $3 OFFSET $4
	`, projectID, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.MyTasks list: %w", err)
	}
	defer rows.Close()

	list := make([]MyTaskRow, 0)
	for rows.Next() {
		var m MyTaskRow
		var nextID, nextName *string
		var nextPic *bool
		if err := rows.Scan(&m.TaskID, &m.TaskCode, &m.Title, &m.ProjectID, &m.StatusID, &m.StatusName, &m.StatusColor, &m.Priority,
			&m.SprintName, &m.DueDate, &m.PicNames, &m.CommentCount, &m.SubTotal, &m.SubDone, &m.LoggedMinutes, &m.EstimateHours,
			&m.IsMine, &m.IsFollowed, &m.IsLate, &nextID, &nextName, &nextPic); err != nil {
			return nil, 0, fmt.Errorf("repository.MyTasks scan: %w", err)
		}
		if nextID != nil && nextName != nil && nextPic != nil {
			m.Next = &MyTaskNextStatus{ID: *nextID, Name: *nextName, RequirePic: *nextPic}
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.MyTasks rows: %w", err)
	}
	return list, total, nil
}

// Stats -- 4 kartu untuk project aktif. BERJALAN = bukan BACKLOG/DONE/CANCELED.
func (r *MyTaskRepository) Stats(ctx context.Context, exec db.Executor, projectID, userID string) (MyTaskStats, error) {
	var s MyTaskStats
	err := exec.QueryRow(ctx, `
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE cs.name NOT IN ('DONE', 'BACKLOG', 'CANCELED'))::int,
		       COALESCE(SUM((SELECT COALESCE(SUM(te.duration_minutes), 0) FROM time_entries te WHERE te.task_id = t.id AND te.is_approved = TRUE)), 0)::int,
		       COUNT(*) FILTER (WHERE `+lateSQL+`)::int
		FROM tasks t JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND `+mineSQL, projectID, userID).
		Scan(&s.Total, &s.Running, &s.LoggedMinutes, &s.Late)
	if err != nil {
		return s, fmt.Errorf("repository.MyTasks stats: %w", err)
	}
	return s, nil
}
