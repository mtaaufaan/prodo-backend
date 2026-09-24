// Package repository -- TimeEntryRepository (Timesheet, dimajukan dari
// Sprint S8 asli ke Track S5/IG-97, US-036/037). Skema `time_entries`/
// `active_timers` sudah terdokumentasi DATABASE_SCHEMA.md §5.31/5.32 dan
// API_CONTRACT.md §15 sejak awal -- migrasi baru dibuat 2026-09-24
// (20261029090000_timesheet), sebelumnya "tabel hantu" sama seperti
// task_version_snapshots (IG-97).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type TimeEntry struct {
	ID              string
	TaskID          string
	UserID          string
	UserName        string
	UserEmail       string
	StartedAt       time.Time
	EndedAt         *time.Time
	DurationMinutes *int
	EntryType       string // 'timer' | 'manual'
	Note            *string
	IsApproved      *bool // nil=pending, true=approved, false=rejected
	RejectionNote   *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ActiveTimer struct {
	UserID    string
	TaskID    string
	TaskCode  *string
	StartedAt time.Time
}

type TimeEntryRepository struct{}

func NewTimeEntryRepository() *TimeEntryRepository {
	return &TimeEntryRepository{}
}

// StartTimer -- PK active_timers=user_id menjamin satu timer per user
// (unique violation -> ErrTimerAlreadyRunning, bukan cross-project/task
// check manual).
func (r *TimeEntryRepository) StartTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*ActiveTimer, error) {
	var startedAt time.Time
	err := exec.QueryRow(ctx, `
		INSERT INTO active_timers (user_id, task_id) VALUES ($1, $2)
		RETURNING started_at
	`, userID, taskID).Scan(&startedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.StartTimer: %w", classifyUniqueViolation(err, domain.ErrTimerAlreadyRunning))
	}
	return &ActiveTimer{UserID: userID, TaskID: taskID, StartedAt: startedAt}, nil
}

// GetActiveTimer -- SATU baris per user (PK), independen dari taskID --
// dipakai StartTimer (409 detail) dan GET .../time-entries/active (cek
// SPESIFIK task ini, caller yang cocokkan TaskID).
func (r *TimeEntryRepository) GetActiveTimer(ctx context.Context, exec db.Executor, userID string) (*ActiveTimer, error) {
	var t ActiveTimer
	err := exec.QueryRow(ctx, `
		SELECT at.user_id, at.task_id, t.task_code, at.started_at
		FROM active_timers at
		JOIN tasks t ON t.id = at.task_id
		WHERE at.user_id = $1
	`, userID).Scan(&t.UserID, &t.TaskID, &t.TaskCode, &t.StartedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("repository.GetActiveTimer: %w", err)
	}
	return &t, nil
}

// StopTimer -- baca+hapus active_timers, insert time_entries entry_type=
// 'timer' is_approved=TRUE (auto-approved, US-036 AC) dalam satu
// transaksi (exec sudah transaksi dari handler DBContextMiddleware).
// taskID divalidasi cocok dengan timer aktif user (ErrNoActiveTimer kalau
// tidak ada timer SAMA SEKALI, atau timer aktif ternyata untuk task lain).
func (r *TimeEntryRepository) StopTimer(ctx context.Context, exec db.Executor, taskID, userID string) (*TimeEntry, error) {
	var startedAt time.Time
	var activeTaskID string
	err := exec.QueryRow(ctx, `DELETE FROM active_timers WHERE user_id = $1 AND task_id = $2 RETURNING task_id, started_at`, userID, taskID).Scan(&activeTaskID, &startedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("repository.StopTimer: %w", domain.ErrNoActiveTimer)
		}
		return nil, fmt.Errorf("repository.StopTimer: %w", err)
	}
	endedAt := time.Now()
	durationMinutes := int(endedAt.Sub(startedAt).Minutes())
	if durationMinutes < 1 {
		durationMinutes = 1
	}
	var e TimeEntry
	e.TaskID, e.UserID, e.StartedAt, e.EndedAt, e.EntryType = taskID, userID, startedAt, &endedAt, "timer"
	approved := true
	e.IsApproved = &approved
	err = exec.QueryRow(ctx, `
		INSERT INTO time_entries (task_id, user_id, started_at, ended_at, duration_minutes, entry_type, is_approved)
		VALUES ($1, $2, $3, $4, $5, 'timer', TRUE)
		RETURNING id, created_at, updated_at
	`, taskID, userID, startedAt, endedAt, durationMinutes).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.StopTimer: insert: %w", err)
	}
	e.DurationMinutes = &durationMinutes
	return &e, nil
}

// CreateManual -- entry_type='manual', is_approved=NULL (pending, US-037
// AC). Overlap dicek di service (query kandidat lalu bandingkan range di
// Go) -- konsisten pola TaskDependencyService.Add yang juga cek circular
// di Go bukan lewat exclusion constraint DB (lebih mudah menghasilkan
// pesan error yang jelas, dan skema §5.31 tidak mendefinisikan EXCLUDE
// constraint apa pun untuk time_entries).
func (r *TimeEntryRepository) CreateManual(ctx context.Context, exec db.Executor, taskID, userID string, startedAt, endedAt time.Time, note *string) (*TimeEntry, error) {
	durationMinutes := int(endedAt.Sub(startedAt).Minutes())
	var e TimeEntry
	e.TaskID, e.UserID, e.StartedAt, e.EndedAt, e.EntryType, e.Note, e.DurationMinutes = taskID, userID, startedAt, &endedAt, "manual", note, &durationMinutes
	err := exec.QueryRow(ctx, `
		INSERT INTO time_entries (task_id, user_id, started_at, ended_at, duration_minutes, entry_type, note, is_approved)
		VALUES ($1, $2, $3, $4, $5, 'manual', $6, NULL)
		RETURNING id, created_at, updated_at
	`, taskID, userID, startedAt, endedAt, durationMinutes, note).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.CreateManual: %w", err)
	}
	return &e, nil
}

// HasOverlap -- rentang [startedAt, endedAt) user ini overlap dengan
// entri time_entries lain (timer maupun manual, status apa pun) --
// excludeID dipakai UpdateManual (jangan bentrok dengan diri sendiri).
func (r *TimeEntryRepository) HasOverlap(ctx context.Context, exec db.Executor, userID string, startedAt, endedAt time.Time, excludeID string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM time_entries
			WHERE user_id = $1 AND id != $2
			  AND started_at < $4 AND COALESCE(ended_at, started_at) > $3
		)
	`, userID, excludeID, startedAt, endedAt).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.HasOverlap: %w", err)
	}
	return exists, nil
}

func (r *TimeEntryRepository) Get(ctx context.Context, exec db.Executor, id string) (*TimeEntry, error) {
	var e TimeEntry
	err := exec.QueryRow(ctx, `
		SELECT id, task_id, user_id, started_at, ended_at, duration_minutes, entry_type, note, is_approved, rejection_note, created_at, updated_at
		FROM time_entries WHERE id = $1
	`, id).Scan(&e.ID, &e.TaskID, &e.UserID, &e.StartedAt, &e.EndedAt, &e.DurationMinutes, &e.EntryType, &e.Note, &e.IsApproved, &e.RejectionNote, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrTaskNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &e, nil
}

// UpdateManual -- hanya dipanggil service setelah verifikasi is_approved
// IS NULL (pending) -- guard DI SINI juga (WHERE is_approved IS NULL)
// sebagai defense-in-depth kalau ada race condition approve bersamaan.
func (r *TimeEntryRepository) UpdateManual(ctx context.Context, exec db.Executor, id string, startedAt, endedAt time.Time, note *string) (*TimeEntry, error) {
	durationMinutes := int(endedAt.Sub(startedAt).Minutes())
	tag, err := exec.Exec(ctx, `
		UPDATE time_entries SET started_at = $2, ended_at = $3, duration_minutes = $4, note = $5, updated_at = NOW()
		WHERE id = $1 AND is_approved IS NULL
	`, id, startedAt, endedAt, durationMinutes, note)
	if err != nil {
		return nil, fmt.Errorf("repository.UpdateManual: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("repository.UpdateManual: %w", domain.ErrTimeEntryAlreadyApproved)
	}
	return r.Get(ctx, exec, id)
}

func (r *TimeEntryRepository) Approve(ctx context.Context, exec db.Executor, id, approvedBy string) error {
	tag, err := exec.Exec(ctx, `UPDATE time_entries SET is_approved = TRUE, updated_at = NOW() WHERE id = $1 AND is_approved IS NULL`, id)
	if err != nil {
		return fmt.Errorf("repository.Approve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Approve: %w", domain.ErrTimeEntryNotPending)
	}
	return nil
}

func (r *TimeEntryRepository) Reject(ctx context.Context, exec db.Executor, id, rejectedBy, note string) error {
	tag, err := exec.Exec(ctx, `UPDATE time_entries SET is_approved = FALSE, rejection_note = $2, updated_at = NOW() WHERE id = $1 AND is_approved IS NULL`, id, note)
	if err != nil {
		return fmt.Errorf("repository.Reject: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Reject: %w", domain.ErrTimeEntryNotPending)
	}
	return nil
}

// ListForTask -- filter opsional approvalStatus ('pending'/'approved'/
// 'rejected') dan userID -- otorisasi (AW/PM lihat semua, lainnya cuma
// diri sendiri) digerbangi di service, bukan di sini.
func (r *TimeEntryRepository) ListForTask(ctx context.Context, exec db.Executor, taskID, approvalStatus, userID string) ([]TimeEntry, error) {
	query := `
		SELECT te.id, te.task_id, te.user_id, COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		       te.started_at, te.ended_at, te.duration_minutes, te.entry_type, te.note, te.is_approved, te.rejection_note, te.created_at, te.updated_at
		FROM time_entries te
		JOIN users u ON u.id = te.user_id
		WHERE te.task_id = $1
	`
	args := []any{taskID}
	if userID != "" {
		args = append(args, userID)
		query += fmt.Sprintf(" AND te.user_id = $%d", len(args))
	}
	switch approvalStatus {
	case "pending":
		query += " AND te.is_approved IS NULL"
	case "approved":
		query += " AND te.is_approved = TRUE"
	case "rejected":
		query += " AND te.is_approved = FALSE"
	}
	query += " ORDER BY te.started_at DESC"

	rows, err := exec.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	defer rows.Close()

	list := make([]TimeEntry, 0)
	for rows.Next() {
		var e TimeEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.UserID, &e.UserName, &e.UserEmail, &e.StartedAt, &e.EndedAt, &e.DurationMinutes, &e.EntryType, &e.Note, &e.IsApproved, &e.RejectionNote, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListForTask: scan: %w", err)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListForTask: %w", err)
	}
	return list, nil
}

// SumLoggedMinutesForTask -- agregat "jam ter-log" (IG-97, header PM Task
// Detail.dc.html hourChip LOGGED/ESTIMASI JAM + field RINGKASAN "JAM
// TERCATAT") -- HANYA entri is_approved=TRUE (timer auto-approved + manual
// yang sudah di-approve), entri pending/rejected TIDAK dihitung supaya
// angka ini tidak bisa dimanipulasi lewat entri manual yang belum
// diverifikasi.
func (r *TimeEntryRepository) SumLoggedMinutesForTask(ctx context.Context, exec db.Executor, taskID string) (int, error) {
	var total int
	if err := exec.QueryRow(ctx, `SELECT COALESCE(SUM(duration_minutes), 0) FROM time_entries WHERE task_id = $1 AND is_approved = TRUE`, taskID).Scan(&total); err != nil {
		return 0, fmt.Errorf("repository.SumLoggedMinutesForTask: %w", err)
	}
	return total, nil
}
