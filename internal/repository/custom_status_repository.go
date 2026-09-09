// Package repository -- CustomStatusRepository (Task Management Core
// Phase 1, forward-pull). Baca-saja untuk sekarang -- CRUD status custom
// (AW/PM Custom Status) adalah scope terpisah (US-019/020), 5 status
// sistem di-seed otomatis saat workspace dibuat (WorkspaceRepository.Create)
// sudah cukup untuk papan Kanban dasar Phase 1.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type CustomStatus struct {
	ID                       string
	ScopeType                string
	ScopeID                  string
	Name                     string
	ColorToken               *string
	Position                 int
	IsSystem                 bool
	IsUndefined              bool
	RequireStartConfirmation bool
	CreatedAt                time.Time
}

type CustomStatusRepository struct{}

func NewCustomStatusRepository() *CustomStatusRepository {
	return &CustomStatusRepository{}
}

const customStatusSelectColumns = `id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, require_start_confirmation, created_at`

func scanCustomStatus(row interface{ Scan(dest ...any) error }) (*CustomStatus, error) {
	var s CustomStatus
	if err := row.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.RequireStartConfirmation, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListForWorkspace -- status board level workspace (project-scoped status
// custom belum dibangun Phase 1, lihat komentar package).
func (r *CustomStatusRepository) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]CustomStatus, error) {
	rows, err := exec.Query(ctx, `
		SELECT `+customStatusSelectColumns+`
		FROM custom_statuses
		WHERE scope_type = 'workspace' AND scope_id = $1
		ORDER BY position ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListForWorkspace: %w", err)
	}
	defer rows.Close()

	list := make([]CustomStatus, 0)
	for rows.Next() {
		s, err := scanCustomStatus(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListForWorkspace: scan: %w", err)
		}
		list = append(list, *s)
	}
	return list, rows.Err()
}

// GetBacklogStatus -- status default task baru (S4-13 AC: "status default
// BACKLOG"), diresolve dari scope workspace pemilik project.
func (r *CustomStatusRepository) GetBacklogStatus(ctx context.Context, exec db.Executor, workspaceID string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `
		SELECT `+customStatusSelectColumns+`
		FROM custom_statuses
		WHERE scope_type = 'workspace' AND scope_id = $1 AND name = 'BACKLOG'
	`, workspaceID)
	s, err := scanCustomStatus(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.GetBacklogStatus: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.GetBacklogStatus: %w", err)
	}
	return s, nil
}

func (r *CustomStatusRepository) Get(ctx context.Context, exec db.Executor, statusID string) (*CustomStatus, error) {
	row := exec.QueryRow(ctx, `SELECT `+customStatusSelectColumns+` FROM custom_statuses WHERE id = $1`, statusID)
	s, err := scanCustomStatus(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return s, nil
}

// SetRequireStartConfirmation -- PUT /statuses/:id (Phase 4, US-018b/S4-64).
func (r *CustomStatusRepository) SetRequireStartConfirmation(ctx context.Context, exec db.Executor, statusID string, require bool) error {
	tag, err := exec.Exec(ctx, `UPDATE custom_statuses SET require_start_confirmation = $2, updated_at = NOW() WHERE id = $1`, statusID, require)
	if err != nil {
		return fmt.Errorf("repository.SetRequireStartConfirmation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetRequireStartConfirmation: %w", domain.ErrCustomStatusNotFound)
	}
	return nil
}
