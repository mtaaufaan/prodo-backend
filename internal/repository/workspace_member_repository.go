// Package repository -- WorkspaceMemberRepository (S2-03, US-002). Tabel
// tenant-scoped (workspace_id), lihat docs/DATABASE_SCHEMA.md §5.10.
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type WorkspaceMemberRepository struct {
	db *pgxpool.Pool
}

func NewWorkspaceMemberRepository(db *pgxpool.Pool) *WorkspaceMemberRepository {
	return &WorkspaceMemberRepository{db: db}
}

// GetRole mengembalikan role user saat ini di workspace -- pgx.ErrNoRows
// (tidak di-wrap ke domain error di sini, dicek via errors.Is oleh
// caller) kalau user belum jadi member.
func (r *WorkspaceMemberRepository) GetRole(ctx context.Context, workspaceID, userID string) (string, error) {
	var role string
	err := r.db.QueryRow(ctx, `
		SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&role)
	if err != nil {
		return "", fmt.Errorf("repository.GetRole: %w", err)
	}
	return role, nil
}

// UpsertRole menetapkan role user di workspace (S2-03) -- INSERT kalau
// belum member, UPDATE role kalau sudah. invited_by TIDAK ditimpa saat
// UPDATE (tetap menyimpan siapa yang pertama kali mengundang, bukan siapa
// yang belakangan mengubah role).
func (r *WorkspaceMemberRepository) UpsertRole(ctx context.Context, workspaceID, userID, role string, invitedBy *string) error {
	if _, err := r.db.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, $3::workspace_role, $4)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, workspaceID, userID, role, invitedBy); err != nil {
		return fmt.Errorf("repository.UpsertRole: %w", err)
	}
	return nil
}
