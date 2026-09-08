// Package repository -- CSVImportRepository (Import Data, Track S4G,
// desain "GA Import Data.dc.html"). Tabel `csv_imports` (migrasi
// 20260918090000) -- kind dibatasi 'member' saja, lihat komentar migrasi.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type CSVImport struct {
	ID           string
	GroupID      string
	OrgID        string
	Kind         string
	ImportedBy   string
	ActorRole    string
	Filename     string
	StorageKey   string
	Status       string
	TotalRows    int
	SuccessCount int
	FailedCount  int
	RowResults   json.RawMessage
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

type CSVImportRepository struct{}

func NewCSVImportRepository() *CSVImportRepository {
	return &CSVImportRepository{}
}

// Create menyimpan baris import BARU status 'pending' -- dipanggil saat
// dry-run/validasi (import belum benar-benar dieksekusi, row_results berisi
// preview hasil validasi).
func (r *CSVImportRepository) Create(ctx context.Context, exec db.Executor, groupID, orgID, importedBy, actorRole, filename, storageKey string, totalRows int, rowResults []byte) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO csv_imports (group_id, org_id, imported_by, actor_role, original_filename, storage_key, total_rows, row_results)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, groupID, orgID, importedBy, actorRole, filename, storageKey, totalRows, rowResults).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.Create: %w", err)
	}
	return id, nil
}

func (r *CSVImportRepository) Get(ctx context.Context, exec db.Executor, importID string) (*CSVImport, error) {
	var m CSVImport
	err := exec.QueryRow(ctx, `
		SELECT id, group_id, org_id, kind, imported_by, actor_role, original_filename, storage_key,
		       status, COALESCE(total_rows,0), COALESCE(success_count,0), COALESCE(failed_count,0),
		       COALESCE(row_results, '[]'), created_at, completed_at
		FROM csv_imports WHERE id = $1
	`, importID).Scan(&m.ID, &m.GroupID, &m.OrgID, &m.Kind, &m.ImportedBy, &m.ActorRole, &m.Filename, &m.StorageKey,
		&m.Status, &m.TotalRows, &m.SuccessCount, &m.FailedCount, &m.RowResults, &m.CreatedAt, &m.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrCSVImportNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &m, nil
}

// ListByGroup mengembalikan riwayat import satu grup, terbaru dulu.
func (r *CSVImportRepository) ListByGroup(ctx context.Context, exec db.Executor, groupID string) ([]CSVImport, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, group_id, org_id, kind, imported_by, actor_role, original_filename, storage_key,
		       status, COALESCE(total_rows,0), COALESCE(success_count,0), COALESCE(failed_count,0),
		       COALESCE(row_results, '[]'), created_at, completed_at
		FROM csv_imports WHERE group_id = $1
		ORDER BY created_at DESC
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListByGroup: %w", err)
	}
	defer rows.Close()

	list := make([]CSVImport, 0)
	for rows.Next() {
		var m CSVImport
		if err := rows.Scan(&m.ID, &m.GroupID, &m.OrgID, &m.Kind, &m.ImportedBy, &m.ActorRole, &m.Filename, &m.StorageKey,
			&m.Status, &m.TotalRows, &m.SuccessCount, &m.FailedCount, &m.RowResults, &m.CreatedAt, &m.CompletedAt); err != nil {
			return nil, fmt.Errorf("repository.ListByGroup: scan: %w", err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListByGroup: %w", err)
	}
	return list, nil
}

// MarkRunning -- dipanggil job Asynq begitu mulai eksekusi (lewat konteks
// bypass platform_admin, lihat migrasi csv_imports_update).
func (r *CSVImportRepository) MarkRunning(ctx context.Context, exec db.Executor, importID string) error {
	tag, err := exec.Exec(ctx, `UPDATE csv_imports SET status = 'running' WHERE id = $1 AND status = 'pending'`, importID)
	if err != nil {
		return fmt.Errorf("repository.MarkRunning: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.MarkRunning: %w", domain.ErrCSVImportNotFound)
	}
	return nil
}

// Complete menyimpan hasil akhir eksekusi -- row_results DITIMPA dengan
// status final per baris (sebelumnya preview dry-run).
func (r *CSVImportRepository) Complete(ctx context.Context, exec db.Executor, importID string, successCount, failedCount int, rowResults []byte) error {
	_, err := exec.Exec(ctx, `
		UPDATE csv_imports SET status = 'completed', success_count = $2, failed_count = $3, row_results = $4, completed_at = NOW()
		WHERE id = $1
	`, importID, successCount, failedCount, rowResults)
	if err != nil {
		return fmt.Errorf("repository.Complete: %w", err)
	}
	return nil
}

func (r *CSVImportRepository) MarkFailed(ctx context.Context, exec db.Executor, importID string) error {
	_, err := exec.Exec(ctx, `UPDATE csv_imports SET status = 'failed', completed_at = NOW() WHERE id = $1`, importID)
	if err != nil {
		return fmt.Errorf("repository.MarkFailed: %w", err)
	}
	return nil
}

// GetUserContact -- email+nama actor untuk isi email invitation (dipakai
// job CSVImportExecute sebagai "inviterName") -- query langsung, pola sama
// RetentionRepository.GetUserContact.
func (r *CSVImportRepository) GetUserContact(ctx context.Context, exec db.Executor, userID string) (email, displayName string, err error) {
	if err := exec.QueryRow(ctx, `SELECT email, display_name FROM users WHERE id = $1`, userID).Scan(&email, &displayName); err != nil {
		return "", "", fmt.Errorf("repository.GetUserContact: %w", err)
	}
	return email, displayName, nil
}
