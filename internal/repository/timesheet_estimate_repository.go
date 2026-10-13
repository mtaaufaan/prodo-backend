// Package repository -- TimesheetEstimateRepository (estimasi jam tercatat
// dari riwayat tanggal status hasil import CSV, IG-129). Sumber: sesi status
// (task_status_sessions) + PIC fase (task_pic_phases) task `is_imported`.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

// EstimateSourceRow -- satu fase yang akan diestimasi.
type EstimateSourceRow struct {
	TaskID   string
	TaskCode string
	Phase    string
	Start    time.Time
	End      *time.Time // nil = fase masih berjalan (BACKLOG nil = belum lengkap -> dilewati service)
	PICs     []string
}

// EstimateInsert -- satu entri estimasi untuk disimpan.
type EstimateInsert struct {
	TaskID    string
	UserID    string
	Phase     string
	StartedAt time.Time
	EndedAt   time.Time
	Minutes   int
}

type TimesheetEstimateRepository struct{}

func NewTimesheetEstimateRepository() *TimesheetEstimateRepository {
	return &TimesheetEstimateRepository{}
}

// realEntryGuard -- task yang SUDAH punya jam nyata (timer/manual orangnya
// sendiri) tidak diestimasi: data nyata menang atas perkiraan.
const realEntryGuard = `NOT EXISTS (SELECT 1 FROM time_entries te WHERE te.task_id = t.id AND NOT te.is_estimate)`

// ListSources -- fase kerja (sesi status selain BACKLOG/DONE/BLOCKED/CANCELED)
// ditambah fase BACKLOG (task dibuat -> dinyatakan lengkap: ready_at, atau
// tanggal keluar sesi BACKLOG untuk task import yang tak punya ready_at).
// BACKLOG hanya untuk task yang pernah melanjut ke fase kerja (task yang
// langsung dibatalkan dari backlog tidak dikreditkan).
func (r *TimesheetEstimateRepository) ListSources(ctx context.Context, exec db.Executor, projectID string) ([]EstimateSourceRow, error) {
	rows, err := exec.Query(ctx, `
		SELECT t.id, COALESCE(t.task_code, ''), cs.name, s.entered_at, s.exited_at,
		       COALESCE((SELECT array_agg(DISTINCT tpp.user_id)::text[] FROM task_pic_phases tpp
		                 WHERE tpp.task_id = t.id AND tpp.status_id = s.status_id), ARRAY[]::text[])
		FROM tasks t
		JOIN task_status_sessions s ON s.task_id = t.id
		JOIN custom_statuses cs ON cs.id = s.status_id
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND t.is_imported
		  AND cs.name NOT IN ('BACKLOG', 'DONE', 'BLOCKED', 'CANCELED')
		  AND `+realEntryGuard+`
		UNION ALL
		SELECT t.id, COALESCE(t.task_code, ''), 'BACKLOG', t.created_at,
		       COALESCE(t.ready_at, (SELECT s2.exited_at FROM task_status_sessions s2 JOIN custom_statuses c2 ON c2.id = s2.status_id
		                             WHERE s2.task_id = t.id AND c2.name = 'BACKLOG' AND s2.exited_at IS NOT NULL
		                             ORDER BY s2.entered_at LIMIT 1)),
		       COALESCE((SELECT array_agg(DISTINCT tpp.user_id)::text[] FROM task_pic_phases tpp JOIN custom_statuses c3 ON c3.id = tpp.status_id
		                 WHERE tpp.task_id = t.id AND c3.name = 'BACKLOG'), ARRAY[]::text[])
		FROM tasks t
		WHERE t.project_id = $1 AND t.deleted_at IS NULL AND t.is_imported
		  AND `+realEntryGuard+`
		  AND EXISTS (SELECT 1 FROM task_status_sessions s3 JOIN custom_statuses c4 ON c4.id = s3.status_id
		              WHERE s3.task_id = t.id AND c4.name NOT IN ('BACKLOG', 'CANCELED'))
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.EstimateListSources: %w", err)
	}
	defer rows.Close()
	list := make([]EstimateSourceRow, 0)
	for rows.Next() {
		var e EstimateSourceRow
		if err := rows.Scan(&e.TaskID, &e.TaskCode, &e.Phase, &e.Start, &e.End, &e.PICs); err != nil {
			return nil, fmt.Errorf("repository.EstimateListSources: scan: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// ReplaceEstimates -- hapus SELURUH entri estimasi task project ini lalu
// tulis ulang (idempoten). Entri: manual, disetujui, is_estimate.
func (r *TimesheetEstimateRepository) ReplaceEstimates(ctx context.Context, exec db.Executor, projectID string, entries []EstimateInsert) error {
	if _, err := exec.Exec(ctx, `
		DELETE FROM time_entries te USING tasks t
		WHERE te.task_id = t.id AND t.project_id = $1 AND te.is_estimate
	`, projectID); err != nil {
		return fmt.Errorf("repository.ReplaceEstimates: hapus: %w", err)
	}
	for i := range entries {
		e := &entries[i]
		if _, err := exec.Exec(ctx, `
			INSERT INTO time_entries (task_id, user_id, started_at, ended_at, duration_minutes, entry_type, is_approved, is_estimate, note, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'manual', TRUE, TRUE, $6, $4, $4)
		`, e.TaskID, e.UserID, e.StartedAt, e.EndedAt, e.Minutes, "[estimasi-import] fase "+e.Phase); err != nil {
			return fmt.Errorf("repository.ReplaceEstimates: tulis: %w", err)
		}
	}
	return nil
}

// LogRecalculation -- audit trail hitung ulang estimasi (entity project).
func (r *TimesheetEstimateRepository) LogRecalculation(ctx context.Context, exec db.Executor, actorID, actorRole, projectID, workspaceID, projectName string, entries, tasks, minutes int) error {
	var meta map[string]any
	if projectName != "" {
		meta = map[string]any{"name": projectName} // kosong -> nama diresolve live-JOIN projects (aman, project soft-delete)
	}
	return insertProjectAudit(ctx, exec, actorID, actorRole, "timesheet.estimates_recalculated", projectID, workspaceID, nil,
		map[string]any{"entries": entries, "tasks": tasks, "minutes": minutes}, meta)
}
