// Package repository -- TaskDependencyRepository (Task Management Core
// Phase 3, US-018: Finish-to-Start Hard-Block). DATABASE_SCHEMA.md §5.17 --
// PK (predecessor_id, successor_id), TIDAK ada kolom id/type terpisah
// seperti teks sprint_backlog.md S4-46 yang basi (satu-satunya jenis
// dependency yang didukung adalah finish-to-start).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type TaskDependency struct {
	PredecessorID         string
	PredecessorCode       *string
	PredecessorTitle      string
	PredecessorStatusName string
	SuccessorID           string
	SuccessorCode         *string
	SuccessorTitle        string
	SuccessorStatusName   string
	CreatedBy             *string
	CreatedAt             time.Time
}

type TaskDependencyRepository struct{}

func NewTaskDependencyRepository() *TaskDependencyRepository {
	return &TaskDependencyRepository{}
}

const taskDependencySelectColumns = `
	td.predecessor_id, tp.task_code, tp.title, cs_p.name,
	td.successor_id, ts.task_code, ts.title, cs_s.name,
	td.created_by, td.created_at
`

func scanTaskDependency(row interface{ Scan(dest ...any) error }) (*TaskDependency, error) {
	var d TaskDependency
	if err := row.Scan(&d.PredecessorID, &d.PredecessorCode, &d.PredecessorTitle, &d.PredecessorStatusName,
		&d.SuccessorID, &d.SuccessorCode, &d.SuccessorTitle, &d.SuccessorStatusName,
		&d.CreatedBy, &d.CreatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *TaskDependencyRepository) queryDependencies(ctx context.Context, exec db.Executor, whereClause string, args ...any) ([]TaskDependency, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+taskDependencySelectColumns+`
		FROM task_dependencies td
		JOIN tasks tp ON tp.id = td.predecessor_id
		JOIN custom_statuses cs_p ON cs_p.id = tp.status_id
		JOIN tasks ts ON ts.id = td.successor_id
		JOIN custom_statuses cs_s ON cs_s.id = ts.status_id
		WHERE `+whereClause, args...)
	if err != nil {
		return nil, fmt.Errorf("queryDependencies: %w", err)
	}
	defer rows.Close()

	list := make([]TaskDependency, 0)
	for rows.Next() {
		d, err := scanTaskDependency(rows)
		if err != nil {
			return nil, fmt.Errorf("queryDependencies: scan: %w", err)
		}
		list = append(list, *d)
	}
	return list, rows.Err()
}

// ListPredecessors -- task yang harus selesai duluan sebelum taskID ini
// (GET /tasks/:id/dependencies "predecessors").
func (r *TaskDependencyRepository) ListPredecessors(ctx context.Context, exec db.Executor, taskID string) ([]TaskDependency, error) {
	return r.queryDependencies(ctx, exec, "td.successor_id = $1", taskID)
}

// ListSuccessors -- task yang diblokir OLEH taskID ini ("successors").
func (r *TaskDependencyRepository) ListSuccessors(ctx context.Context, exec db.Executor, taskID string) ([]TaskDependency, error) {
	return r.queryDependencies(ctx, exec, "td.predecessor_id = $1", taskID)
}

// ListIncompletePredecessors -- predecessor taskID yang BELUM berstatus
// DONE (US-018, S4-48 HARD-BLOCK: dicek sebelum status task ini berubah).
func (r *TaskDependencyRepository) ListIncompletePredecessors(ctx context.Context, exec db.Executor, taskID string) ([]TaskDependency, error) {
	return r.queryDependencies(ctx, exec, "td.successor_id = $1 AND cs_p.name != 'DONE'", taskID)
}

// WouldCreateCycle -- deteksi circular dependency (S4-47) via recursive CTE
// SEBELUM insert: menambahkan edge (predecessorID -> successorID) membentuk
// lingkaran jika predecessorID SUDAH bisa dicapai dari successorID lewat
// rantai dependency yang ada (successorID -> ... -> predecessorID),
// menutup lingkaran predecessorID -> successorID -> ... -> predecessorID.
// Path (array task_id) dikembalikan cuma kalau lingkaran terdeteksi --
// dipakai membangun pesan CIRCULAR_DEPENDENCY (task_code per node).
func (r *TaskDependencyRepository) WouldCreateCycle(ctx context.Context, exec db.Executor, predecessorID, successorID string) ([]string, error) {
	rows, err := exec.Query(ctx, `
		WITH RECURSIVE reachable AS (
			SELECT successor_id AS node, ARRAY[predecessor_id, successor_id] AS path
			FROM task_dependencies WHERE predecessor_id = $1
			UNION ALL
			SELECT td.successor_id, r.path || td.successor_id
			FROM task_dependencies td
			JOIN reachable r ON td.predecessor_id = r.node
			WHERE NOT td.successor_id = ANY(r.path)
		)
		SELECT path FROM reachable WHERE node = $2 LIMIT 1
	`, successorID, predecessorID)
	if err != nil {
		return nil, fmt.Errorf("repository.WouldCreateCycle: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, rows.Err()
	}
	var path []string
	if err := rows.Scan(&path); err != nil {
		return nil, fmt.Errorf("repository.WouldCreateCycle: scan: %w", err)
	}
	// Tutup lingkaran secara visual: path SUDAH berujung di predecessorID
	// (node target); tambahkan successorID (start) supaya jelas kembali ke
	// titik awal -- predecessorID -> successorID -> ... -> predecessorID.
	return append(path, successorID), nil
}

// Create -- tambah dependency (US-018, S4-51). Pemanggil (service) WAJIB
// sudah lolos cek self-reference dan WouldCreateCycle sebelum ini.
func (r *TaskDependencyRepository) Create(ctx context.Context, exec db.Executor, predecessorID, successorID string, createdBy *string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO task_dependencies (predecessor_id, successor_id, created_by)
		VALUES ($1, $2, $3)
	`, predecessorID, successorID, createdBy)
	if err != nil {
		return fmt.Errorf("repository.Create: %w", classifyUniqueViolation(err, domain.ErrDependencyAlreadyExists))
	}
	return nil
}

func (r *TaskDependencyRepository) Delete(ctx context.Context, exec db.Executor, predecessorID, successorID string) error {
	tag, err := exec.Exec(ctx, `DELETE FROM task_dependencies WHERE predecessor_id = $1 AND successor_id = $2`, predecessorID, successorID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrDependencyNotFound)
	}
	return nil
}

// NotifySuccessorPics -- S4-50: notify PIC aktif tiap successor LANGSUNG
// taskID ini saat predecessor pindah ke DONE (unblocked=true, tipe
// dependency_unblocked) atau keluar dari DONE (unblocked=false, tipe
// dependency_blocked). Satu INSERT...SELECT untuk semua successor+PIC
// sekaligus, bukan loop per baris.
//
// ponytail: TIDAK mengecek apakah successor itu masih punya predecessor
// lain yang belum DONE (agregat blocking penuh) -- sesuai teks AC literal
// S4-50 ("predecessor berubah ke DONE -> notif ke PIC penerus"), notifikasi
// dikirim per-predecessor bukan per-status-blocking-gabungan. Kalau
// notifikasi "positif palsu" ini jadi masalah (successor sebenarnya masih
// diblokir predecessor lain), tambah filter EXISTS (predecessor lain belum
// DONE) di WHERE.
func (r *TaskDependencyRepository) NotifySuccessorPics(ctx context.Context, exec db.Executor, taskID string, unblocked bool) error {
	notifType, title, body := "dependency_blocked", "Dependency Task Diblokir Kembali", "Predecessor task Anda berpindah keluar dari DONE -- task ini kembali terblokir."
	if unblocked {
		notifType, title, body = "dependency_unblocked", "Dependency Task Selesai", "Predecessor task ini sudah DONE -- task Anda mungkin tidak lagi terblokir."
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
		SELECT DISTINCT tpp.user_id, NULL::uuid, $2, 'task', td.successor_id, $3, $4
		FROM task_dependencies td
		JOIN task_pic_phases tpp ON tpp.task_id = td.successor_id AND tpp.is_active = TRUE
		WHERE td.predecessor_id = $1
	`, taskID, notifType, title, body)
	if err != nil {
		return fmt.Errorf("repository.NotifySuccessorPics: %w", err)
	}
	return nil
}
