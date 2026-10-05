// Package repository -- ProjectImportRepository (Import CSV PM, tahap (a)
// import sprint, "PM Import CSV.dc.html", IG-120). Tabel `project_imports`
// (migrasi 20261111090000) -- level PROJECT, terpisah dari `csv_imports`
// (level group, kind member). Eksekusi sinkron di request, jadi cuma
// status pending/completed/failed.
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

type ProjectImport struct {
	ID             string
	ProjectID      string
	Kind           string
	ImportedBy     string
	ImportedByName string
	ActorRole      string
	Filename       string
	Status         string
	TotalRows      int
	SuccessCount   *int
	FailedCount    *int
	RowResults     json.RawMessage
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

type ProjectImportRepository struct{}

func NewProjectImportRepository() *ProjectImportRepository { return &ProjectImportRepository{} }

func (r *ProjectImportRepository) Create(ctx context.Context, exec db.Executor, projectID, kind, importedBy, actorRole, filename string, totalRows int, rowResults []byte) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO project_imports (project_id, kind, imported_by, actor_role, original_filename, total_rows, row_results)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, projectID, kind, importedBy, actorRole, filename, totalRows, rowResults).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.ProjectImport.Create: %w", err)
	}
	return id, nil
}

const projectImportColumns = `pi.id, pi.project_id, pi.kind, pi.imported_by, COALESCE(NULLIF(u.display_name, ''), u.email, ''),
	pi.actor_role, pi.original_filename, pi.status, pi.total_rows, pi.success_count, pi.failed_count, pi.created_at, pi.completed_at`

func scanProjectImport(row pgx.Row, withRows bool) (*ProjectImport, error) {
	var m ProjectImport
	dest := []any{&m.ID, &m.ProjectID, &m.Kind, &m.ImportedBy, &m.ImportedByName, &m.ActorRole, &m.Filename, &m.Status,
		&m.TotalRows, &m.SuccessCount, &m.FailedCount, &m.CreatedAt, &m.CompletedAt}
	if withRows {
		dest = append(dest, &m.RowResults)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return &m, nil
}

// Get -- satu import LENGKAP dengan row_results (dipakai Execute/Report).
func (r *ProjectImportRepository) Get(ctx context.Context, exec db.Executor, importID string) (*ProjectImport, error) {
	m, err := scanProjectImport(exec.QueryRow(ctx, `
		SELECT `+projectImportColumns+`, pi.row_results
		FROM project_imports pi LEFT JOIN users u ON u.id = pi.imported_by
		WHERE pi.id = $1
	`, importID), true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.ProjectImport.Get: %w", domain.ErrCSVImportNotFound)
		}
		return nil, fmt.Errorf("repository.ProjectImport.Get: %w", err)
	}
	return m, nil
}

// ListByProject -- riwayat tanpa row_results (ringan); terbaru dulu.
// Import yang masih 'pending' (cuma lolos pratinjau, tidak pernah
// dijalankan) tidak ditampilkan di riwayat.
func (r *ProjectImportRepository) ListByProject(ctx context.Context, exec db.Executor, projectID string) ([]ProjectImport, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+projectImportColumns+`
		FROM project_imports pi LEFT JOIN users u ON u.id = pi.imported_by
		WHERE pi.project_id = $1 AND pi.status <> 'pending'
		ORDER BY pi.created_at DESC
		LIMIT 100
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.ProjectImport.ListByProject: %w", err)
	}
	defer rows.Close()
	list := make([]ProjectImport, 0)
	for rows.Next() {
		m, err := scanProjectImport(rows, false)
		if err != nil {
			return nil, fmt.Errorf("repository.ProjectImport.ListByProject: scan: %w", err)
		}
		list = append(list, *m)
	}
	return list, rows.Err()
}

// Complete -- tandai selesai + simpan hasil per baris + audit
// project_import.created (satu entri per import, bukan per baris).
func (r *ProjectImportRepository) Complete(ctx context.Context, exec db.Executor, imp *ProjectImport, successCount, failedCount int, rowResults []byte, workspaceID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE project_imports SET status = 'completed', success_count = $2, failed_count = $3, row_results = $4, completed_at = NOW()
		WHERE id = $1 AND status = 'pending'
	`, imp.ID, successCount, failedCount, rowResults)
	if err != nil {
		return fmt.Errorf("repository.ProjectImport.Complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.ProjectImport.Complete: %w", domain.ErrCSVImportAlreadyStarted)
	}
	return insertProjectImportAudit(ctx, exec, actorID, actorRole, "project_import.created", imp, workspaceID,
		map[string]any{"success_count": successCount, "failed_count": failedCount})
}

// AuditReportDownload -- unduh log baris dilewati tercatat di Audit Trail
// ("PM Import CSV.dc.html": "Tercatat di Audit Trail").
func (r *ProjectImportRepository) AuditReportDownload(ctx context.Context, exec db.Executor, imp *ProjectImport, workspaceID, actorID, actorRole string) error {
	return insertProjectImportAudit(ctx, exec, actorID, actorRole, "project_import.report_downloaded", imp, workspaceID, nil)
}

// insertProjectImportAudit -- snapshot immutable di metadata (name = nama
// berkas, kind, project_id) -- project_imports ikut terhapus bersama
// project (ON DELETE CASCADE), jadi live JOIN tidak boleh jadi satu-satunya
// sumber nama (pelajaran IG-26/IG-29).
func insertProjectImportAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action string, imp *ProjectImport, workspaceID string, extra map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{"name": imp.Filename, "kind": imp.Kind, "project_id": imp.ProjectID}
	if path != "" {
		metadata["request_path"] = path
	}
	for k, v := range extra {
		metadata[k] = v
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertProjectImportAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, metadata)
		VALUES ($1, $2, $3, 'project_import', $4, $5, $6::inet, $7)
	`, actorID, actorRole, action, imp.ID, workspaceID, ip, metaJSON)
	return err
}
