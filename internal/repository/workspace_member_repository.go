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

// MembershipRow -- satu baris hasil ListMembershipsForUser (S16-01,
// forward-pull Track S4G: GET /me/context).
type MembershipRow struct {
	WorkspaceID string
	Name        string
	OrgName     string
	Role        string
}

// ListMembershipsForUser mengembalikan seluruh workspace tempat user ini
// jadi member -- dipakai switcher context GA dual-role ("PINDAH WORKSPACE").
// RLS aman tanpa bypass: setiap baris hasil query ini adalah baris milik
// user_id itu sendiri, jadi prodo_is_workspace_member(workspace_id) selalu
// bernilai true untuknya (lihat wm_select).
func (r *WorkspaceMemberRepository) ListMembershipsForUser(ctx context.Context, exec db.Executor, userID string) ([]MembershipRow, error) {
	rows, err := exec.Query(ctx, `
		SELECT wm.workspace_id, w.name, o.name, wm.role
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN organizations o ON o.id = w.org_id
		WHERE wm.user_id = $1
		ORDER BY o.name, w.name
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListMembershipsForUser: %w", err)
	}
	defer rows.Close()

	var result []MembershipRow
	for rows.Next() {
		var m MembershipRow
		if err := rows.Scan(&m.WorkspaceID, &m.Name, &m.OrgName, &m.Role); err != nil {
			return nil, fmt.Errorf("repository.ListMembershipsForUser: scan: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMembershipsForUser: rows: %w", err)
	}
	return result, nil
}

// ProjectScopedMembership -- satu project tempat user ini jadi member
// project-scoped (project_members ATAU project_managers), TANPA baris
// workspace_members untuk workspace pemiliknya (susulan, ditemukan user
// lewat gap-check: project-scoped member tidak pernah muncul di
// ListMembershipsForUser di atas, bikin Home.tsx/WorkspaceLayout dead-end
// buat mereka -- lihat migrasi 20261105090000). SENGAJA daftar TERPISAH
// dari MembershipRow, TIDAK menyalakan switcher multi-workspace
// (dikonfirmasi user) -- cuma dipakai landing (Home.tsx, kalau
// MembershipRow kosong) dan fallback resolusi role (WorkspaceLayout,
// dicocokkan ke project aktif di URL).
type ProjectScopedMembership struct {
	ProjectID     string
	ProjectName   string
	WorkspaceID   string
	WorkspaceName string
	OrgName       string
	Role          string
}

// ListProjectScopedMembershipsForUser -- lihat komentar ProjectScopedMembership.
// PM TIDAK PERNAH project-scoped-only (AddMembersBulk/CreateBulkInvitations
// selalu mewajibkan workspace_members untuk role project_manager, lihat
// komentar projectScopedBulkRoles) -- klausa project_managers di sini
// murni jaga-jaga struktural (skema polymorphic yang sama), bukan jalur
// yang benar-benar dipakai hari ini.
func (r *WorkspaceMemberRepository) ListProjectScopedMembershipsForUser(ctx context.Context, exec db.Executor, userID string) ([]ProjectScopedMembership, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT p.id, p.name, p.workspace_id, w.name, o.name,
		  CASE WHEN pmg.user_id IS NOT NULL THEN 'project_manager' ELSE pm.role::text END
		FROM projects p
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN organizations o ON o.id = w.org_id
		LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $1
		LEFT JOIN project_managers pmg ON pmg.project_id = p.id AND pmg.user_id = $1
		WHERE (pm.user_id = $1 OR pmg.user_id = $1)
		  AND p.deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM workspace_members wm WHERE wm.workspace_id = p.workspace_id AND wm.user_id = $1
		  )
		ORDER BY o.name, w.name, p.name
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListProjectScopedMembershipsForUser: %w", err)
	}
	defer rows.Close()

	result := make([]ProjectScopedMembership, 0)
	for rows.Next() {
		var m ProjectScopedMembership
		if err := rows.Scan(&m.ProjectID, &m.ProjectName, &m.WorkspaceID, &m.WorkspaceName, &m.OrgName, &m.Role); err != nil {
			return nil, fmt.Errorf("repository.ListProjectScopedMembershipsForUser: scan: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListProjectScopedMembershipsForUser: rows: %w", err)
	}
	return result, nil
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
	// actor_ip/metadata.request_path (implementation_gaps.md IG-64) --
	// sebelumnya tidak pernah diisi, kolom ASAL di GA/AW Audit Trail kosong
	// untuk aksi ganti role workspace.
	ip, path := requestMetaFromContext(ctx)
	var metaJSON []byte
	if path != "" {
		if metaJSON, err = marshalIfNotEmpty(map[string]any{"request_path": path}); err != nil {
			return fmt.Errorf("repository.AssignRole: encode metadata: %w", err)
		}
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, 'member.role_changed', 'workspace_member', $3, $4, $5::inet, $6::jsonb, $7::jsonb, $8)
	`, actorID, actorRole, userID, workspaceID, ip, beforeJSON, afterJSON, metaJSON); err != nil {
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
	Title       *string
	Role        string
	JoinedAt    time.Time
	// ProjectNames -- nama project (dipisah ", ", urut abjad) tempat user
	// ini punya keterkaitan project-level di workspace INI -- PM lewat
	// project_managers (susulan multi-PM), editor/approver/viewer lewat
	// project_members (S4W susulan role restructuring, 2026-09-14). Kosong
	// untuk role
	// workspace-scoped murni (admin_workspace/division_viewer) atau kalau
	// belum ditautkan ke project mana pun.
	ProjectNames string
	// ProjectID -- ID project PERTAMA (urut abjad, sama urutan dengan
	// ProjectNames) dari keterkaitan di atas -- dipakai FE (ManageMemberPanel)
	// pre-fill pemilih project saat Kelola dibuka, ditemukan user 2026-09-14
	// ("dropdown project juga tidak terbinding") karena ProjectNames cuma
	// nama tampilan, bukan ID yang bisa dipilih ulang. Kosong kalau
	// ProjectNames juga kosong. Kalau user punya LEBIH dari satu
	// keterkaitan project (jarang -- PM di >1 project), cuma project
	// PERTAMA yang di-pre-fill; menyimpan lewat panel ini tetap "move" ke
	// SATU project terpilih (behavior tidak berubah dari sebelumnya).
	ProjectID string
}

// ListMembers mengembalikan seluruh member LANGSUNG workspace (S2-07/08
// prasyarat -- S3-14 asli minta dua array workspace_members+
// project_scoped_members; array kedua itu sekarang ListProjectScopedMembers
// di bawah, dipanggil terpisah oleh WorkspaceHandler.ListMembers).
func (r *WorkspaceMemberRepository) ListMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]Member, error) {
	rows, err := exec.Query(ctx, `
		SELECT wm.user_id, u.email, u.display_name, u.title, wm.role, wm.joined_at,
		       COALESCE(proj.names, ''), COALESCE(proj.first_id::text, '')
		FROM workspace_members wm
		JOIN users u ON u.id = wm.user_id
		LEFT JOIN LATERAL (
			SELECT string_agg(DISTINCT p.name, ', ' ORDER BY p.name) AS names,
			       (array_agg(p.id ORDER BY p.name))[1] AS first_id
			FROM projects p
			WHERE p.workspace_id = wm.workspace_id AND p.deleted_at IS NULL
			  AND (
			    EXISTS (SELECT 1 FROM project_managers pmg WHERE pmg.project_id = p.id AND pmg.user_id = wm.user_id)
			    OR EXISTS (SELECT 1 FROM project_members pmem WHERE pmem.project_id = p.id AND pmem.user_id = wm.user_id)
			  )
		) proj ON true
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
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Title, &m.Role, &m.JoinedAt, &m.ProjectNames, &m.ProjectID); err != nil {
			return nil, fmt.Errorf("repository.ListMembers: scan: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMembers: rows: %w", err)
	}
	return members, nil
}

// ProjectScopedMember -- satu baris member project-scoped-only
// (project_members, TANPA baris workspace_members) di SATU project
// workspace ini -- ditampilkan di halaman Member & Roles AW-facing
// (WorkspaceMembersPage) berdampingan dengan ListMembers di atas,
// dikonfirmasi user "tampil dan bisa dikelola penuh dari sini juga". Satu
// baris per (project, user) -- kalau user scoped di >1 project workspace
// ini, muncul >1 baris; Kelola/Keluarkan FE tetap lewat endpoint
// /projects/:id/members/:userId yang sudah ada (ProjectMemberService),
// BUKAN endpoint /workspaces/:wsId/members/:userId (yang butuh baris
// workspace_members) -- lihat komentar WorkspaceHandler.ListMembers.
type ProjectScopedMember struct {
	UserID      string
	Email       string
	DisplayName string
	Title       *string
	Role        string
	ProjectID   string
	ProjectName string
	AddedAt     time.Time
}

// ListProjectScopedMembers mengembalikan seluruh project_members (BUKAN
// project_managers -- PM tidak pernah project-scoped-only, lihat komentar
// ListProjectScopedMembershipsForUser) di project-project milik
// workspaceID yang usernya TIDAK punya baris workspace_members sama
// sekali.
func (r *WorkspaceMemberRepository) ListProjectScopedMembers(ctx context.Context, exec db.Executor, workspaceID string) ([]ProjectScopedMember, error) {
	rows, err := exec.Query(ctx, `
		SELECT u.id, u.email, u.display_name, u.title, pm.role, p.id, p.name, pm.added_at
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN users u ON u.id = pm.user_id
		WHERE p.workspace_id = $1 AND p.deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM workspace_members wm WHERE wm.workspace_id = $1 AND wm.user_id = pm.user_id
		  )
		ORDER BY pm.added_at ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListProjectScopedMembers: %w", err)
	}
	defer rows.Close()

	members := make([]ProjectScopedMember, 0)
	for rows.Next() {
		var m ProjectScopedMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Title, &m.Role, &m.ProjectID, &m.ProjectName, &m.AddedAt); err != nil {
			return nil, fmt.Errorf("repository.ListProjectScopedMembers: scan: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListProjectScopedMembers: rows: %w", err)
	}
	return members, nil
}

// ListOrgCandidates mengembalikan member unik lintas SELURUH workspace
// dalam satu organisasi (S4G-05, Track S4G, desain "GA Add Workspace.dc.html"
// picker "MEMBER YANG ADA" + dropdown ganti admin di "GA Workspaces.dc.html").
// workspace_members cuma berisi member yang SUDAH menerima undangan (baris
// PENDING hidup di user_invitations, tabel terpisah) -- jadi query ini
// otomatis memenuhi syarat desain "status AKTIF" tanpa filter tambahan.
func (r *WorkspaceMemberRepository) ListOrgCandidates(ctx context.Context, exec db.Executor, orgID string) ([]Member, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.email, u.display_name
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN users u ON u.id = wm.user_id
		WHERE w.org_id = $1
		ORDER BY u.display_name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListOrgCandidates: %w", err)
	}
	defer rows.Close()

	list := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName); err != nil {
			return nil, fmt.Errorf("repository.ListOrgCandidates: scan: %w", err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListOrgCandidates: %w", err)
	}
	return list, nil
}

// CountAdminsExcluding menghitung member admin_workspace di workspace ini,
// TIDAK termasuk excludeUserID -- dipakai guard "minimal satu admin_workspace
// harus tersisa" (S4W-01) sebelum RemoveMember menghapus atau AssignRole
// menurunkan role admin_workspace terakhir.
func (r *WorkspaceMemberRepository) CountAdminsExcluding(ctx context.Context, exec db.Executor, workspaceID, excludeUserID string) (int, error) {
	var count int
	if err := exec.QueryRow(ctx, `
		SELECT count(*) FROM workspace_members
		WHERE workspace_id = $1 AND role = 'admin_workspace' AND user_id != $2
	`, workspaceID, excludeUserID).Scan(&count); err != nil {
		return 0, fmt.Errorf("repository.CountAdminsExcluding: %w", err)
	}
	return count, nil
}

// ListWorkspaceMemberCandidates mengembalikan member organisasi pemilik
// workspaceID yang BELUM jadi member workspace ini -- "pool kandidat" di
// modal Undang Member Admin Workspace (S4W-02, desain "AW Invite
// Member.dc.html"), beda dari ListOrgCandidates (S4G-05, dipakai GA/PA,
// tidak mengecualikan member workspace target manapun).
func (r *WorkspaceMemberRepository) ListWorkspaceMemberCandidates(ctx context.Context, exec db.Executor, orgID, workspaceID string) ([]Member, error) {
	rows, err := exec.Query(ctx, `
		SELECT DISTINCT u.id, u.email, u.display_name
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN users u ON u.id = wm.user_id
		WHERE w.org_id = $1
		  AND NOT EXISTS (
		    SELECT 1 FROM workspace_members wm2
		    WHERE wm2.workspace_id = $2 AND wm2.user_id = u.id
		  )
		ORDER BY u.display_name
	`, orgID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListWorkspaceMemberCandidates: %w", err)
	}
	defer rows.Close()

	list := make([]Member, 0)
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName); err != nil {
			return nil, fmt.Errorf("repository.ListWorkspaceMemberCandidates: scan: %w", err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListWorkspaceMemberCandidates: %w", err)
	}
	return list, nil
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

	ip, path := requestMetaFromContext(ctx)
	var metaJSON []byte
	if path != "" {
		encoded, err := marshalIfNotEmpty(map[string]any{"request_path": path})
		if err != nil {
			return fmt.Errorf("repository.RemoveMember: encode metadata: %w", err)
		}
		metaJSON = encoded
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, metadata)
		VALUES ($1, $2, 'member.removed', 'workspace_member', $3, $4, $5::inet, $6)
	`, actorID, actorRole, userID, workspaceID, ip, metaJSON); err != nil {
		return fmt.Errorf("repository.RemoveMember: audit: %w", err)
	}
	return nil
}
