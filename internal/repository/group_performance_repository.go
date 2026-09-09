// Package repository -- GroupPerformanceRepository (Performance Dashboard
// Lintas Organisasi, Track S4G, desain "GA Kinerja Grup.dc.html", US-079/
// S4G-25). Baca-saja di atas tasks/task_status_sessions -- agregasi
// (completion rate, on-time rate, bottleneck) dihitung di Go (service),
// bukan SQL besar, karena volume task per grup realistis kecil di v1
// (pola sama TaskRepository.List soal assignee -- satu query agregat per
// keperluan, bukan N+1, tapi bukan pula satu query monster).
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// TaskMetric -- satu baris task untuk agregasi completion/on-time/overdue.
type TaskMetric struct {
	OrgID       string
	Priority    string
	StatusName  string
	DueDate     *time.Time
	CompletedAt *time.Time
}

// StatusDwell -- satu sesi status untuk agregasi bottleneck (durasi hunian,
// dalam hari). Sesi yang masih aktif (exited_at NULL) dihitung sampai NOW().
type StatusDwell struct {
	OrgID      string
	StatusName string
	DwellDays  float64
}

type GroupPerformanceRepository struct{}

func NewGroupPerformanceRepository() *GroupPerformanceRepository {
	return &GroupPerformanceRepository{}
}

// scopeClause -- filter dasar dipakai kedua query di bawah: grup + org
// aktif + org_id opsional + rentang waktu opsional (kolom rentang beda per
// pemanggil -- `timeColumn`). Dynamic WHERE (bukan parameter NULL bertipe
// uuid) supaya orgID kosong tidak perlu ditangani lewat cast/NULL trick --
// pola sama GroupAuditLogFilter.buildWhere.
func scopeClause(groupID, orgID, timeColumn string, since *time.Time) (where string, args []any) {
	clauses := []string{"o.group_id = $1", "o.deactivated_at IS NULL", "t.deleted_at IS NULL"}
	args = []any{groupID}
	n := 1
	if orgID != "" {
		n++
		clauses = append(clauses, fmt.Sprintf("o.id = $%d", n))
		args = append(args, orgID)
	}
	if since != nil {
		n++
		clauses = append(clauses, fmt.Sprintf("%s >= $%d", timeColumn, n))
		args = append(args, *since)
	}
	return strings.Join(clauses, " AND "), args
}

// ListTaskMetrics -- seluruh task dalam grup (org aktif saja), difilter
// org_id opsional dan "dibuat sejak" opsional (RENTANG di desain).
func (r *GroupPerformanceRepository) ListTaskMetrics(ctx context.Context, exec db.Executor, groupID, orgID string, since *time.Time) ([]TaskMetric, error) {
	where, args := scopeClause(groupID, orgID, "t.created_at", since)
	rows, err := exec.Query(ctx, `
		SELECT o.id, t.priority, cs.name, t.due_date, t.completed_at
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN organizations o ON o.id = w.org_id
		JOIN custom_statuses cs ON cs.id = t.status_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListTaskMetrics: %w", err)
	}
	defer rows.Close()

	list := make([]TaskMetric, 0)
	for rows.Next() {
		var m TaskMetric
		if err := rows.Scan(&m.OrgID, &m.Priority, &m.StatusName, &m.DueDate, &m.CompletedAt); err != nil {
			return nil, fmt.Errorf("repository.ListTaskMetrics: scan: %w", err)
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// ListStatusDwell -- durasi hunian tiap sesi status WIP (BACKLOG/IN
// PROGRESS/UNDER REVIEW/BLOCKED -- DONE dikecualikan, tidak ada "hunian"
// yang relevan lagi setelah selesai, sama pola desain "GA Kinerja
// Grup.dc.html" STATUSES) untuk bottleneck detection. Rentang waktu
// difilter dari `entered_at` sesi (bukan `created_at` task seperti
// ListTaskMetrics) supaya sesi lama task lama yang masih aktif ikut
// terhitung kalau baru masuk status itu dalam rentang.
func (r *GroupPerformanceRepository) ListStatusDwell(ctx context.Context, exec db.Executor, groupID, orgID string, since *time.Time) ([]StatusDwell, error) {
	where, args := scopeClause(groupID, orgID, "tss.entered_at", since)
	where += " AND cs.name IN ('BACKLOG', 'IN PROGRESS', 'UNDER REVIEW', 'BLOCKED')"

	rows, err := exec.Query(ctx, `
		SELECT o.id, cs.name,
		       EXTRACT(EPOCH FROM (COALESCE(tss.exited_at, NOW()) - tss.entered_at)) / 86400.0
		FROM task_status_sessions tss
		JOIN tasks t ON t.id = tss.task_id
		JOIN projects p ON p.id = t.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN organizations o ON o.id = w.org_id
		JOIN custom_statuses cs ON cs.id = tss.status_id
		WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListStatusDwell: %w", err)
	}
	defer rows.Close()

	list := make([]StatusDwell, 0)
	for rows.Next() {
		var d StatusDwell
		if err := rows.Scan(&d.OrgID, &d.StatusName, &d.DwellDays); err != nil {
			return nil, fmt.Errorf("repository.ListStatusDwell: scan: %w", err)
		}
		list = append(list, d)
	}
	return list, rows.Err()
}
