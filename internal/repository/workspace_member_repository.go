// Package repository -- WorkspaceMemberRepository (S2-03/05/06, US-002).
// Tabel tenant-scoped (workspace_id), lihat docs/DATABASE_SCHEMA.md §5.10.
package repository

import (
	"context"
	"encoding/json"
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

// AssignRole menetapkan role user di workspace (S2-03), mencatat audit
// trail (S2-06), dan mengirim in-app notification ke target (S2-05) --
// SATU transaksi, semua-atau-tidak-sama-sekali. Awalnya tiga query
// terpisah tanpa transaksi -- gagal di step notifikasi (mis. tabel belum
// ada) tetap meninggalkan role_members ter-update walau response ke
// client 500, ketahuan saat verifikasi live. before nil kalau target
// sebelumnya belum jadi member (tidak ada "state sebelum" yang berarti).
func (r *WorkspaceMemberRepository) AssignRole(
	ctx context.Context,
	workspaceID, userID, role string,
	invitedBy *string,
	actorID, actorRole string,
	before, after map[string]string,
	notifTitle, notifBody string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.AssignRole: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	if _, err := tx.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, $3::workspace_role, $4)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, workspaceID, userID, role, invitedBy); err != nil {
		return fmt.Errorf("repository.AssignRole: upsert role: %w", err)
	}

	var beforeJSON, afterJSON []byte
	if before != nil {
		if beforeJSON, err = json.Marshal(before); err != nil {
			return fmt.Errorf("repository.AssignRole: marshal state_before: %w", err)
		}
	}
	if afterJSON, err = json.Marshal(after); err != nil {
		return fmt.Errorf("repository.AssignRole: marshal state_after: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, state_before, state_after)
		VALUES ($1, $2, 'member.role_changed', 'workspace_member', $3, $4, $5::jsonb, $6::jsonb)
	`, actorID, actorRole, userID, workspaceID, beforeJSON, afterJSON); err != nil {
		return fmt.Errorf("repository.AssignRole: audit: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, title, body)
		VALUES ($1, $2, 'role_changed', $3, $4)
	`, userID, actorID, notifTitle, notifBody); err != nil {
		return fmt.Errorf("repository.AssignRole: notifikasi: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.AssignRole: commit: %w", err)
	}
	return nil
}
