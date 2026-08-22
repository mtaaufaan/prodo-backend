// Package repository -- InvitationRepository (S2-16/17/18, US-006).
// Tabel tenant-scoped (workspace_id), lihat docs/DATABASE_SCHEMA.md §5.30.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
)

type InvitationRepository struct{}

func NewInvitationRepository() *InvitationRepository {
	return &InvitationRepository{}
}

// CreateInvitation menyimpan satu undangan (token plaintext TIDAK pernah
// disimpan -- hanya hash-nya, lihat DATABASE_SCHEMA.md §5.30).
func (r *InvitationRepository) CreateInvitation(
	ctx context.Context,
	exec db.Executor,
	email, workspaceID, role, invitedByUserID, tokenHash string,
	expiresAt time.Time,
) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO user_invitations (email, workspace_id, role, invited_by, token_hash, expires_at)
		VALUES ($1, $2, $3::workspace_role, $4, $5, $6)
		RETURNING id
	`, email, workspaceID, role, invitedByUserID, tokenHash, expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("repository.CreateInvitation: %w", err)
	}
	return id, nil
}
