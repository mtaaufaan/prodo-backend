// Package repository -- WorkspaceRepository (S3-09, US-008). Tabel
// `workspaces` (DATABASE_SCHEMA.md §5.9), beda dari WorkspaceMemberRepository
// yang menangani tabel workspace_members. Kena RLS sejak S3-42, jadi terima
// db.Executor per-panggilan (pola sama OrganizationRepository).
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

type WorkspaceRepository struct{}

func NewWorkspaceRepository() *WorkspaceRepository {
	return &WorkspaceRepository{}
}

// Workspace -- subset kolom §5.9 yang dipakai response S3-09/10/11/12.
type Workspace struct {
	ID         string
	OrgID      string
	Name       string
	ArchivedAt *time.Time
	CreatedAt  time.Time
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

	if err := insertWorkspaceAudit(ctx, exec, actorID, actorRole, "workspace.created", ws.ID, orgID); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}
	return ws, nil
}

// GetOrgID mengembalikan org_id pemilik workspaceID -- dipakai
// WorkspaceService.DeleteWorkspace untuk resolve org yang harus dicek
// OrganizationService.AuthorizeOrgAccess (DELETE workspace HANYA boleh
// Platform Admin/Group Admin pemilik org, BUKAN Admin Workspace -- lihat
// migrations/20260827090000_rls_organizations_workspaces.up.sql
// workspaces_delete yang sengaja tidak punya cabang prodo_is_workspace_member).
func (r *WorkspaceRepository) GetOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error) {
	var orgID string
	err := exec.QueryRow(ctx, `SELECT org_id FROM workspaces WHERE id = $1`, workspaceID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.GetOrgID: %w", domain.ErrWorkspaceNotFound)
		}
		return "", fmt.Errorf("repository.GetOrgID: %w", err)
	}
	return orgID, nil
}

// Update mengubah name workspace (S3-10) -- DATABASE_SCHEMA.md §5.9 tidak
// punya kolom deskripsi/avatar seperti wording asli task ini, cuma `name`
// (dan `mention_cooldown_minutes`, di luar scope S3-10).
func (r *WorkspaceRepository) Update(ctx context.Context, exec db.Executor, workspaceID, name, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE workspaces SET name = $2, updated_at = NOW()
		WHERE id = $1
	`, workspaceID, name)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrWorkspaceNotFound)
	}

	orgID, err := r.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	if err := insertWorkspaceAudit(ctx, exec, actorID, actorRole, "workspace.updated", workspaceID, orgID); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// Deactivate menyetel archived_at (S3-11) -- soft, akses SELURUH member
// diblokir (US-008 AC), data tetap tersimpan. Tidak idempotent, sama pola
// OrganizationRepository.Deactivate.
func (r *WorkspaceRepository) Deactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	return r.setArchived(ctx, exec, workspaceID, actorID, actorRole, true)
}

// Reactivate mengosongkan archived_at (kebalikan Deactivate).
func (r *WorkspaceRepository) Reactivate(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	return r.setArchived(ctx, exec, workspaceID, actorID, actorRole, false)
}

func (r *WorkspaceRepository) setArchived(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string, archive bool) error {
	action := "workspace.reactivated"
	var tag pgconn.CommandTag
	var err error
	if archive {
		action = "workspace.deactivated"
		tag, err = exec.Exec(ctx, `
			UPDATE workspaces SET archived_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND archived_at IS NULL
		`, workspaceID)
	} else {
		tag, err = exec.Exec(ctx, `
			UPDATE workspaces SET archived_at = NULL, updated_at = NOW()
			WHERE id = $1 AND archived_at IS NOT NULL
		`, workspaceID)
	}
	if err != nil {
		return fmt.Errorf("repository.setArchived: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.setArchived: %w", domain.ErrWorkspaceNotFound)
	}

	orgID, err := r.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.setArchived: %w", err)
	}
	if err := insertWorkspaceAudit(ctx, exec, actorID, actorRole, action, workspaceID, orgID); err != nil {
		return fmt.Errorf("repository.setArchived: audit: %w", err)
	}
	return nil
}

// Delete menghapus workspace permanen (S3-12). ⚠️ Wording task asli
// ("soft-delete", "hanya jika semua project dihapus", "row tetap ada di DB
// dengan deleted_at") TIDAK COCOK skema: §5.9 tidak punya kolom deleted_at
// (cuma archived_at, sudah dipakai Deactivate di atas untuk arti berbeda),
// dan tabel `projects` BELUM ADA sama sekali sampai sprint ini (migrasi
// baru menyusul S3-19+, lihat implementation_gaps.md IG-17) -- guard "semua
// project dihapus" TIDAK BISA diimplementasikan sekarang. Direalisasikan
// sebagai hard DELETE tanpa guard project (sama pola OrganizationRepository.
// Delete yang JUGA hard DELETE walau wording task-nya juga bilang "soft"),
// cascade ke workspace_members via FK. Guard project menyusul begitu tabel
// projects ada -- dicatat IG-17, JANGAN didiamkan.
func (r *WorkspaceRepository) Delete(ctx context.Context, exec db.Executor, workspaceID, actorID, actorRole string) error {
	orgID, err := r.GetOrgID(ctx, exec, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}

	if err := insertWorkspaceAudit(ctx, exec, actorID, actorRole, "workspace.deleted", workspaceID, orgID); err != nil {
		return fmt.Errorf("repository.Delete: audit: %w", err)
	}

	tag, err := exec.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrWorkspaceNotFound)
	}
	return nil
}

// List mengembalikan workspace dalam satu organisasi (S3-13 prasyarat).
// Scoping tambahan lewat RLS `workspaces_select` (S3-42) -- Platform
// Admin/Group Admin pemilik org lihat semua, workspace member biasa cuma
// lihat workspace-nya sendiri (baris lain difilter diam-diam oleh RLS,
// bukan filter WHERE eksplisit di sini).
func (r *WorkspaceRepository) List(ctx context.Context, exec db.Executor, orgID string) ([]Workspace, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, org_id, name, archived_at, created_at
		FROM workspaces
		WHERE org_id = $1
		ORDER BY name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	list := make([]Workspace, 0)
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.OrgID, &w.Name, &w.ArchivedAt, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	return list, nil
}

func insertWorkspaceAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, workspaceID, orgID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id, workspace_id)
		VALUES ($1, $2, $3, 'workspace', $4, $5, $4)
	`, actorID, actorRole, action, workspaceID, orgID)
	return err
}
