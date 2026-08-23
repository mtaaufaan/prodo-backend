// Package repository -- WorkspaceRepository (S3-09, US-008). Tabel
// `workspaces` (DATABASE_SCHEMA.md §5.9), beda dari WorkspaceMemberRepository
// yang menangani tabel workspace_members. Kena RLS sejak S3-42, jadi terima
// db.Executor per-panggilan (pola sama OrganizationRepository).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type WorkspaceRepository struct{}

func NewWorkspaceRepository() *WorkspaceRepository {
	return &WorkspaceRepository{}
}

// Workspace -- subset kolom §5.9 yang dipakai response S3-09.
type Workspace struct {
	ID        string
	OrgID     string
	Name      string
	CreatedAt time.Time
}

// Create menyimpan workspace baru + audit trail (S3-09). Assignment Admin
// Workspace (AW) dilakukan CALLER (WorkspaceService) lewat
// RBACService.AssignRole yang sudah ada -- reuse logic S2-03, bukan
// duplikasi insert workspace_members di sini.
func (r *WorkspaceRepository) Create(ctx context.Context, exec db.Executor, orgID, name, actorID, actorRole string) (*Workspace, error) {
	ws := &Workspace{OrgID: orgID, Name: name}
	err := exec.QueryRow(ctx, `
		INSERT INTO workspaces (org_id, name)
		VALUES ($1, $2)
		RETURNING id, created_at
	`, orgID, name).Scan(&ws.ID, &ws.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id, workspace_id)
		VALUES ($1, $2, 'workspace.created', 'workspace', $3, $4, $3)
	`, actorID, actorRole, ws.ID, orgID); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}
	return ws, nil
}
