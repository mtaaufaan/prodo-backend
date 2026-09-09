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
	ID          string
	ScopeType   string
	ScopeID     string
	Name        string
	ColorToken  *string
	Position    int
	IsSystem    bool
	IsUndefined bool
	CreatedAt   time.Time
}

type CustomStatusRepository struct{}

func NewCustomStatusRepository() *CustomStatusRepository {
	return &CustomStatusRepository{}
}

// ListForWorkspace -- status board level workspace (project-scoped status
// custom belum dibangun Phase 1, lihat komentar package).
func (r *CustomStatusRepository) ListForWorkspace(ctx context.Context, exec db.Executor, workspaceID string) ([]CustomStatus, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, created_at
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
		var s CustomStatus
		if err := rows.Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListForWorkspace: scan: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// GetBacklogStatus -- status default task baru (S4-13 AC: "status default
// BACKLOG"), diresolve dari scope workspace pemilik project.
func (r *CustomStatusRepository) GetBacklogStatus(ctx context.Context, exec db.Executor, workspaceID string) (*CustomStatus, error) {
	var s CustomStatus
	err := exec.QueryRow(ctx, `
		SELECT id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, created_at
		FROM custom_statuses
		WHERE scope_type = 'workspace' AND scope_id = $1 AND name = 'BACKLOG'
	`, workspaceID).Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.GetBacklogStatus: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.GetBacklogStatus: %w", err)
	}
	return &s, nil
}

func (r *CustomStatusRepository) Get(ctx context.Context, exec db.Executor, statusID string) (*CustomStatus, error) {
	var s CustomStatus
	err := exec.QueryRow(ctx, `
		SELECT id, scope_type, scope_id, name, color_token, position, is_system, is_undefined, created_at
		FROM custom_statuses WHERE id = $1
	`, statusID).Scan(&s.ID, &s.ScopeType, &s.ScopeID, &s.Name, &s.ColorToken, &s.Position, &s.IsSystem, &s.IsUndefined, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.Get: %w", domain.ErrCustomStatusNotFound)
		}
		return nil, fmt.Errorf("repository.Get: %w", err)
	}
	return &s, nil
}
