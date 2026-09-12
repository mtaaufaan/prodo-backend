// Package repository -- GroupMemberRepository (Members & Roles, forward-pull
// US-086 + Track S4G): direktori member GROUP-WIDE (lintas semua organisasi/
// workspace dalam satu grup) dan mutasi terkait (toggle Eksekutif, identitas,
// aktif/nonaktif akun). users/group_admin_assignments/executive_assignments
// TIDAK ber-RLS (pola sama account_repository.go) -- exec di sini boleh
// request-scoped tx (dbCtx, dipakai query workspace_members/workspaces/
// organizations yang BER-RLS) atau pool biasa, keduanya valid untuk tabel
// tanpa RLS.
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

type GroupMemberRepository struct{}

func NewGroupMemberRepository() *GroupMemberRepository {
	return &GroupMemberRepository{}
}

// GroupMemberRow -- satu user (GROUP ADMIN/EKSEKUTIF/member biasa) yang
// terhubung ke grup ini lewat group_admin_assignments, executive_assignments,
// ATAU workspace_members (workspace mana pun dalam grup). Roles diisi
// terpisah oleh service (join Go-side dengan ListMemberWorkspaceRoles,
// hindari N+1 query per user).
type GroupMemberRow struct {
	UserID       string
	Email        string
	DisplayName  string
	Title        *string
	IsActive     bool
	SuspendedAt  *time.Time
	IsGroupAdmin bool
	IsExecutive  bool
}

func (r *GroupMemberRepository) ListMembers(ctx context.Context, exec db.Executor, groupID string) ([]GroupMemberRow, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.email, u.display_name, u.title, u.is_active, u.suspended_at,
		       (gaa.user_id IS NOT NULL), (ea.user_id IS NOT NULL)
		FROM users u
		LEFT JOIN group_admin_assignments gaa ON gaa.user_id = u.id AND gaa.group_id = $1
		LEFT JOIN executive_assignments ea ON ea.user_id = u.id AND ea.group_id = $1
		WHERE gaa.user_id IS NOT NULL
		   OR ea.user_id IS NOT NULL
		   OR EXISTS (
		     SELECT 1 FROM workspace_members wm
		     JOIN workspaces w ON w.id = wm.workspace_id
		     JOIN organizations o ON o.id = w.org_id
		     WHERE wm.user_id = u.id AND o.group_id = $1
		   )
		ORDER BY u.display_name
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListMembers: %w", err)
	}
	defer rows.Close()

	var result []GroupMemberRow
	for rows.Next() {
		var m GroupMemberRow
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Title, &m.IsActive, &m.SuspendedAt,
			&m.IsGroupAdmin, &m.IsExecutive); err != nil {
			return nil, fmt.Errorf("repository.ListMembers: scan: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMembers: rows: %w", err)
	}
	return result, nil
}

// MemberWorkspaceRole -- satu baris role per-workspace, dipakai service
// untuk mengelompokkan per user_id (satu user bisa punya banyak baris,
// lintas organisasi dalam grup yang sama).
type MemberWorkspaceRole struct {
	UserID        string
	WorkspaceID   string
	WorkspaceName string
	OrgName       string
	Role          string
}

func (r *GroupMemberRepository) ListMemberWorkspaceRoles(ctx context.Context, exec db.Executor, groupID string) ([]MemberWorkspaceRole, error) {
	rows, err := exec.Query(ctx, `
		SELECT wm.user_id, w.id, w.name, o.name, wm.role
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN organizations o ON o.id = w.org_id
		WHERE o.group_id = $1
		ORDER BY o.name, w.name
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListMemberWorkspaceRoles: %w", err)
	}
	defer rows.Close()

	var result []MemberWorkspaceRole
	for rows.Next() {
		var m MemberWorkspaceRole
		if err := rows.Scan(&m.UserID, &m.WorkspaceID, &m.WorkspaceName, &m.OrgName, &m.Role); err != nil {
			return nil, fmt.Errorf("repository.ListMemberWorkspaceRoles: scan: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMemberWorkspaceRoles: rows: %w", err)
	}
	return result, nil
}

// isMemberOfGroup -- guard dipakai SEMUA mutasi di bawah supaya GA tidak
// bisa menargetkan user_id di luar grupnya sendiri lewat tebak ID (sama
// klausa EXISTS dengan ListMembers, tapi untuk SATU user).
const isMemberOfGroupSQL = `(
	EXISTS (SELECT 1 FROM group_admin_assignments WHERE user_id = $1 AND group_id = $2)
	OR EXISTS (SELECT 1 FROM executive_assignments WHERE user_id = $1 AND group_id = $2)
	OR EXISTS (
		SELECT 1 FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN organizations o ON o.id = w.org_id
		WHERE wm.user_id = $1 AND o.group_id = $2
	)
)`

// AssignExecutive menandai user sebagai Eksekutif grup ini -- platform_role
// user diubah jadi 'executive' (dipakai middleware.RequireExecutive nanti,
// S16-08) HANYA kalau saat ini 'member' (GA/PA tidak bisa dijadikan
// Eksekutif lewat jalur ini -- dicegah juga di layer service via
// GroupMemberRow.IsGroupAdmin, ini pengaman kedua). Idempotent: ON CONFLICT
// DO NOTHING kalau sudah ter-assign sebelumnya.
func (r *GroupMemberRepository) AssignExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID string) error {
	target, err := exec.Exec(ctx, `
		UPDATE users SET platform_role = 'executive' WHERE id = $1 AND platform_role = 'member'
	`, userID)
	if err != nil {
		return fmt.Errorf("repository.AssignExecutive: update platform_role: %w", err)
	}
	if target.RowsAffected() == 0 {
		// Sudah 'executive' (assign ulang idempotent) ATAU platform_role
		// bukan 'member' sama sekali (GA/PA, ditolak) -- bedakan lewat cek
		// singkat, bukan asumsikan idempotent begitu saja.
		var role string
		if err := exec.QueryRow(ctx, `SELECT platform_role FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
			return fmt.Errorf("repository.AssignExecutive: %w", domain.ErrUserNotFound)
		}
		if role != "executive" {
			return fmt.Errorf("repository.AssignExecutive: %w", domain.ErrForbidden)
		}
	}

	if _, err := exec.Exec(ctx, `
		INSERT INTO executive_assignments (user_id, group_id, assigned_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) DO NOTHING
	`, userID, groupID, actorID); err != nil {
		return fmt.Errorf("repository.AssignExecutive: insert: %w", err)
	}

	if err := insertGroupMemberAudit(ctx, exec, actorID, "member.executive_assigned", userID, groupID, nil, nil); err != nil {
		return fmt.Errorf("repository.AssignExecutive: %w", err)
	}
	return nil
}

// RevokeExecutive mencabut assignment Eksekutif untuk grup ini SAJA --
// platform_role cuma dikembalikan ke 'member' kalau user tidak lagi punya
// executive_assignments di grup LAIN mana pun (§5.38: satu Eksekutif bisa
// di-assign lintas banyak grup).
func (r *GroupMemberRepository) RevokeExecutive(ctx context.Context, exec db.Executor, userID, groupID, actorID string) error {
	tag, err := exec.Exec(ctx, `
		DELETE FROM executive_assignments WHERE user_id = $1 AND group_id = $2
	`, userID, groupID)
	if err != nil {
		return fmt.Errorf("repository.RevokeExecutive: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.RevokeExecutive: %w", domain.ErrUserNotFound)
	}

	var remaining int
	if err := exec.QueryRow(ctx, `SELECT COUNT(*) FROM executive_assignments WHERE user_id = $1`, userID).Scan(&remaining); err != nil {
		return fmt.Errorf("repository.RevokeExecutive: count: %w", err)
	}
	if remaining == 0 {
		if _, err := exec.Exec(ctx, `
			UPDATE users SET platform_role = 'member' WHERE id = $1 AND platform_role = 'executive'
		`, userID); err != nil {
			return fmt.Errorf("repository.RevokeExecutive: update platform_role: %w", err)
		}
	}

	if err := insertGroupMemberAudit(ctx, exec, actorID, "member.executive_revoked", userID, groupID, nil, nil); err != nil {
		return fmt.Errorf("repository.RevokeExecutive: %w", err)
	}
	return nil
}

// UpdateIdentity mengubah nama tampilan + jabatan -- HANYA berlaku untuk
// target yang sedang jadi Eksekutif grup ini (WHERE executive_assignments
// match), sesuai desain "GA Members Roles.dc.html" (panel identitas cuma
// muncul untuk baris Eksekutif).
func (r *GroupMemberRepository) UpdateIdentity(ctx context.Context, exec db.Executor, userID, groupID, actorID, displayName, title string) error {
	// Nilai lama diambil dulu (2026-09-12, implementation_gaps.md IG-64
	// catatan sampingan: UpdateIdentity TIDAK PERNAH menulis audit sama
	// sekali, ditemukan user saat menelusuri gap ASAL/nilai sebelum-sesudah
	// yang sama di helper lain) -- guard isMemberOfGroupSQL yang sama
	// dipakai di sini supaya SELECT dan UPDATE konsisten menegakkan GA cuma
	// boleh menargetkan member dalam grupnya.
	var oldDisplayName string
	var oldTitle *string
	if err := exec.QueryRow(ctx, `
		SELECT display_name, title FROM users WHERE id = $1 AND `+isMemberOfGroupSQL, userID, groupID).Scan(&oldDisplayName, &oldTitle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.UpdateIdentity: %w", domain.ErrUserNotFound)
		}
		return fmt.Errorf("repository.UpdateIdentity: %w", err)
	}

	// Title sekarang kolom umum di users (migrasi 20260916090000) -- berlaku
	// untuk SEMUA member, bukan cuma Eksekutif (konsolidasi dari
	// executive_assignments.title yang sudah dihapus). Guard keanggotaan
	// grup dipertahankan lewat isMemberOfGroupSQL yang sama dengan
	// SetAccess -- GA cuma boleh mengedit identitas member DALAM grupnya.
	tag, err := exec.Exec(ctx, `
		UPDATE users SET display_name = $3, title = $4 WHERE id = $1 AND `+isMemberOfGroupSQL, userID, groupID, displayName, title)
	if err != nil {
		return fmt.Errorf("repository.UpdateIdentity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateIdentity: %w", domain.ErrUserNotFound)
	}

	oldTitleStr := ""
	if oldTitle != nil {
		oldTitleStr = *oldTitle
	}
	before := map[string]any{"display_name": oldDisplayName, "title": oldTitleStr}
	after := map[string]any{"display_name": displayName, "title": title}
	if err := insertGroupMemberAudit(ctx, exec, actorID, "member.identity_updated", userID, groupID, before, after); err != nil {
		return fmt.Errorf("repository.UpdateIdentity: audit: %w", err)
	}
	return nil
}

// SetAccess mengaktifkan/menonaktifkan akun member GROUP-WIDE (users.
// suspended_at, pola sama Suspend/ReactivateGroupAdmin) -- digerbangi guard
// isMemberOfGroupSQL supaya GA tidak bisa menyasar user di luar grupnya.
func (r *GroupMemberRepository) SetAccess(ctx context.Context, exec db.Executor, userID, groupID, actorID string, active bool) error {
	var query, action string
	if active {
		query = `UPDATE users SET suspended_at = NULL WHERE id = $1 AND ` + isMemberOfGroupSQL
		action = "member.reactivated"
	} else {
		query = `UPDATE users SET suspended_at = NOW() WHERE id = $1 AND ` + isMemberOfGroupSQL
		action = "member.suspended"
	}

	tag, err := exec.Exec(ctx, query, userID, groupID)
	if err != nil {
		return fmt.Errorf("repository.SetAccess: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetAccess: %w", domain.ErrUserNotFound)
	}

	if err := insertGroupMemberAudit(ctx, exec, actorID, action, userID, groupID, nil, nil); err != nil {
		return fmt.Errorf("repository.SetAccess: %w", err)
	}
	return nil
}

// insertGroupMemberAudit -- actor_ip/metadata.request_path
// (implementation_gaps.md IG-64) ditambahkan 2026-09-12, sebelumnya tidak
// pernah diisi. stateBefore/stateAfter (nil untuk aksi boolean flip yang
// namanya sudah menjelaskan diri sendiri -- assigned/revoked/reactivated/
// suspended) ditambahkan sekalian untuk UpdateIdentity (ganti nama/jabatan
// Eksekutif) yang SEBELUMNYA TIDAK MENULIS AUDIT SAMA SEKALI (catatan
// sampingan IG-64, dikonfirmasi user untuk diperbaiki).
func insertGroupMemberAudit(ctx context.Context, exec db.Executor, actorID, action, targetUserID, groupID string, stateBefore, stateAfter map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{"group_id": groupID}
	if path != "" {
		metadata["request_path"] = path
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertGroupMemberAudit: encode metadata: %w", err)
	}
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertGroupMemberAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertGroupMemberAudit: encode state_after: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, entity_type, entity_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, 'user', $3, $4::inet, $5, $6, $7)
	`, actorID, action, targetUserID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}
