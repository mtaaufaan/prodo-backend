// Package repository -- WorkspaceMemberRepository (S2-03/05/06, US-002).
// Tabel tenant-scoped (workspace_id), lihat docs/DATABASE_SCHEMA.md §5.10.
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mtaaufaan/prodo-backend/internal/db"
	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// WorkspaceMemberRepository tidak menyimpan *pgxpool.Pool -- setiap method
// menerima db.Executor sebagai parameter (S2-10/11). Executor sebenarnya
// adalah transaksi request-scoped dari middleware.DBContextMiddleware yang
// sudah membawa session variable RLS (app.current_user_id/
// app.current_platform_role); memakai pool langsung di sini akan membuat
// SET LOCAL di middleware tidak pernah terpasang di koneksi yang benar-
// benar dipakai query ini (lihat RLS_DESIGN.md §5.3).
type WorkspaceMemberRepository struct{}

func NewWorkspaceMemberRepository() *WorkspaceMemberRepository {
	return &WorkspaceMemberRepository{}
}

// GetRole mengembalikan role user saat ini di workspace -- pgx.ErrNoRows
// (tidak di-wrap ke domain error di sini, dicek via errors.Is oleh
// caller) kalau user belum jadi member.
func (r *WorkspaceMemberRepository) GetRole(ctx context.Context, exec db.Executor, workspaceID, userID string) (string, error) {
	var role string
	err := exec.QueryRow(ctx, `
		SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID).Scan(&role)
	if err != nil {
		return "", fmt.Errorf("repository.GetRole: %w", err)
	}
	return role, nil
}

// GetWorkspaceOrgID mengembalikan organizations.id pemilik workspaceID
// (S3-41, implementation_gaps.md IG-01) -- dasar scoping Group Admin di
// middleware.RequireRole. `workspaces` BELUM di-RLS (S3-42 menyusul), tapi
// query tetap lewat `exec` yang sama (transaksi request-scoped) supaya
// konsisten dengan pola satu koneksi per request.
func (r *WorkspaceMemberRepository) GetWorkspaceOrgID(ctx context.Context, exec db.Executor, workspaceID string) (string, error) {
	var orgID string
	err := exec.QueryRow(ctx, `SELECT org_id FROM workspaces WHERE id = $1`, workspaceID).Scan(&orgID)
	if err != nil {
		return "", fmt.Errorf("repository.GetWorkspaceOrgID: %w", err)
	}
	return orgID, nil
}

// AssignRole menetapkan role user di workspace (S2-03), mencatat audit
// trail (S2-06), dan mengirim in-app notification ke target (S2-05).
// Atomicity SEKARANG dijamin oleh transaksi request-scoped yang dibawa
// exec (middleware.DBContextMiddleware, S2-11), bukan transaksi lokal di
// sini lagi -- sebelum S2-11 method ini membuka tx sendiri (lihat riwayat
// git), tapi dengan exec yang sudah pasti berupa tx per-request, membuka
// tx bersarang lagi tidak perlu (dan pgx tidak mendukung nested
// transaction sungguhan). before nil kalau target sebelumnya belum jadi
// member (tidak ada "state sebelum" yang berarti).
func (r *WorkspaceMemberRepository) AssignRole(
	ctx context.Context,
	exec db.Executor,
	workspaceID, userID, role string,
	invitedBy *string,
	actorID, actorRole string,
	before, after map[string]string,
	notifTitle, notifBody string,
) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		VALUES ($1, $2, $3::workspace_role, $4)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, workspaceID, userID, role, invitedBy); err != nil {
		return fmt.Errorf("repository.AssignRole: upsert role: %w", err)
	}

	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		if beforeJSON, err = json.Marshal(before); err != nil {
			return fmt.Errorf("repository.AssignRole: marshal state_before: %w", err)
		}
	}
	if afterJSON, err = json.Marshal(after); err != nil {
		return fmt.Errorf("repository.AssignRole: marshal state_after: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, state_before, state_after)
		VALUES ($1, $2, 'member.role_changed', 'workspace_member', $3, $4, $5::jsonb, $6::jsonb)
	`, actorID, actorRole, userID, workspaceID, beforeJSON, afterJSON); err != nil {
		return fmt.Errorf("repository.AssignRole: audit: %w", err)
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, title, body)
		VALUES ($1, $2, 'role_changed', $3, $4)
	`, userID, actorID, notifTitle, notifBody); err != nil {
		return fmt.Errorf("repository.AssignRole: notifikasi: %w", err)
	}

	return nil
}

// Member -- satu baris hasil ListMembers.
type Member struct {
	UserID      string
	Email       string
	DisplayName string
	Role        string
	JoinedAt    time.Time
}

// ListMembers mengembalikan seluruh member LANGSUNG workspace (S2-07/08
// prasyarat -- S3-14 asli minta dua array workspace_members+
// project_scoped_members, tapi konsep project-scoped member butuh tabel
// yang belum ada di S2; cuma workspace_members dulu, cukup untuk
// RolePickerModal S2-07/08).
func (r *WorkspaceMemberRepository) ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]Member, error) {
	rows, err := exec.Query(ctx, `
		SELECT wm.user_id, u.email, u.display_name, wm.role, wm.joined_at
		FROM workspace_members wm
		JOIN users u ON u.id = wm.user_id
		WHERE wm.workspace_id = $1
		ORDER BY wm.joined_at ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListMembers: %w", err)
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("repository.ListMembers: scan: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMembers: rows: %w", err)
	}
	return members, nil
}

// RemoveMember menghapus satu baris workspace_members (S3-15) + audit
// trail. Akun (`users`) itu sendiri TIDAK disentuh -- cuma mencabut
// keanggotaan workspace ini (US-009 AC: "akun masih ada di accounts").
func (r *WorkspaceMemberRepository) RemoveMember(ctx context.Context, exec db.Executor, workspaceID, userID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("repository.RemoveMember: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.RemoveMember: %w", domain.ErrMemberNotFound)
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id)
		VALUES ($1, $2, 'member.removed', 'workspace_member', $3, $4)
	`, actorID, actorRole, userID, workspaceID); err != nil {
		return fmt.Errorf("repository.RemoveMember: audit: %w", err)
	}
	return nil
}
