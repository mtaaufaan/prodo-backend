// Package repository -- ProjectMemberRepository (S3-21/22/23/25/26/27,
// US-009b). Tabel `projects`/`project_members` kena RLS sejak
// 20260829100000_rls_projects (forward-pull, implementation_gaps.md IG-17)
// -- terima db.Executor per-panggilan, pola sama repository lain sejak
// S2-10/11.
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

type ProjectMemberRepository struct{}

func NewProjectMemberRepository() *ProjectMemberRepository {
	return &ProjectMemberRepository{}
}

// ProjectMember -- satu baris hasil ListMembers/GetMember.
type ProjectMember struct {
	ProjectID string
	UserID    string
	Email     string
	Name      string
	Role      string
	IsScoped  bool
	AddedAt   time.Time
}

// CrossOrgMembership -- satu baris hasil ListCrossOrgMemberships (S3-25).
type CrossOrgMembership struct {
	UserID      string
	Email       string
	DisplayName string
	Role        string
	OrgID       string
	OrgName     string
	ProjectID   string
	ProjectName string
}

// GetWorkspaceID mengembalikan workspace_id pemilik projectID -- dasar
// resolve otorisasi (PM/AW workspace mana yang harus dicek) di service.
func (r *ProjectMemberRepository) GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error) {
	var workspaceID string
	err := exec.QueryRow(ctx, `SELECT workspace_id FROM projects WHERE id = $1`, projectID).Scan(&workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.GetWorkspaceID: %w", domain.ErrProjectNotFound)
		}
		return "", fmt.Errorf("repository.GetWorkspaceID: %w", err)
	}
	return workspaceID, nil
}

// AddMember menambahkan project member (S3-21) + audit trail + notifikasi
// AW kalau cross-org (S3-17). isScoped ditentukan CALLER (service)
// berdasarkan apakah targetnya sudah workspace member -- lihat
// DATABASE_SCHEMA.md §5.13 catatan is_scoped. Notifikasi AW (S3-17) hanya
// dikirim saat isScoped true (PM tambah member DARI LUAR workspace) --
// kalau AW belum ada (workspace baru/belum ditunjuk), dilewati diam-diam,
// bukan menggagalkan seluruh operasi tambah member.
func (r *ProjectMemberRepository) AddMember(ctx context.Context, exec db.Executor, projectID, workspaceID, userID, role string, isScoped bool, addedBy, actorRole string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, is_scoped, added_by)
		VALUES ($1, $2, $3::project_scoped_role, $4, $5)
	`, projectID, userID, role, isScoped, addedBy)
	if err != nil {
		return fmt.Errorf("repository.AddMember: %w", classifyUniqueViolation(err, domain.ErrProjectMemberAlreadyExists))
	}

	if err := insertProjectMemberAudit(ctx, exec, addedBy, actorRole, "project_member.added", projectID, userID); err != nil {
		return fmt.Errorf("repository.AddMember: audit: %w", err)
	}

	if isScoped {
		var awUserID string
		err := exec.QueryRow(ctx, `
			SELECT user_id FROM workspace_members WHERE workspace_id = $1 AND role = 'admin_workspace' LIMIT 1
		`, workspaceID).Scan(&awUserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.AddMember: cari admin_workspace: %w", err)
		}
		if awUserID != "" {
			if _, err := exec.Exec(ctx, `
				INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
				VALUES ($1, $2, 'project_scoped_member_added', 'project', $3, 'Member Baru Lintas Organisasi', 'Project Manager menambahkan member dari luar workspace ke salah satu project Anda.')
			`, awUserID, addedBy, projectID); err != nil {
				return fmt.Errorf("repository.AddMember: notifikasi AW: %w", err)
			}
		}
	}
	return nil
}

// UpdateRole mengubah role project member existing (S3-22).
func (r *ProjectMemberRepository) UpdateRole(ctx context.Context, exec db.Executor, projectID, userID, role, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE project_members SET role = $3::project_scoped_role
		WHERE project_id = $1 AND user_id = $2
	`, projectID, userID, role)
	if err != nil {
		return fmt.Errorf("repository.UpdateRole: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateRole: %w", domain.ErrProjectMemberNotFound)
	}

	if err := insertProjectMemberAudit(ctx, exec, actorID, actorRole, "project_member.role_changed", projectID, userID); err != nil {
		return fmt.Errorf("repository.UpdateRole: audit: %w", err)
	}
	return nil
}

// RemoveMember mengeluarkan member dari project (S3-23) -- TIDAK
// menyentuh workspace_members (US-009b AC).
func (r *ProjectMemberRepository) RemoveMember(ctx context.Context, exec db.Executor, projectID, userID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		DELETE FROM project_members WHERE project_id = $1 AND user_id = $2
	`, projectID, userID)
	if err != nil {
		return fmt.Errorf("repository.RemoveMember: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.RemoveMember: %w", domain.ErrProjectMemberNotFound)
	}

	if err := insertProjectMemberAudit(ctx, exec, actorID, actorRole, "project_member.removed", projectID, userID); err != nil {
		return fmt.Errorf("repository.RemoveMember: audit: %w", err)
	}
	return nil
}

// ListMembers mengembalikan seluruh project member (dipakai FE S3-24).
func (r *ProjectMemberRepository) ListMembers(ctx context.Context, exec db.Executor, projectID string) ([]ProjectMember, error) {
	rows, err := exec.Query(ctx, `
		SELECT pm.project_id, pm.user_id, u.email, u.display_name, pm.role, pm.is_scoped, pm.added_at
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1
		ORDER BY pm.added_at ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListMembers: %w", err)
	}
	defer rows.Close()

	members := make([]ProjectMember, 0)
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Email, &m.Name, &m.Role, &m.IsScoped, &m.AddedAt); err != nil {
			return nil, fmt.Errorf("repository.ListMembers: scan: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListMembers: %w", err)
	}
	return members, nil
}

// ListCrossOrgMemberships mengembalikan project-scoped member (is_scoped =
// TRUE) dalam satu grup, opsional difilter per organisasi (S3-25/27).
// TIDAK butuh SECURITY DEFINER bypass seperti GroupRepository.SearchAccounts
// (S3-20) -- Group Admin SUDAH punya visibility penuh lintas org dalam
// grupnya lewat RLS pm_select (prodo_is_group_admin_of_project), jadi query
// polos lewat exec RLS-aware sudah cukup discoped dengan benar.
func (r *ProjectMemberRepository) ListCrossOrgMemberships(ctx context.Context, exec db.Executor, groupID, orgIDFilter string) ([]CrossOrgMembership, error) {
	rows, err := exec.Query(ctx, `
		SELECT pm.user_id, u.email, u.display_name, pm.role, o.id, o.name, p.id, p.name
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		JOIN projects p ON p.id = pm.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN organizations o ON o.id = w.org_id
		WHERE o.group_id = $1 AND pm.is_scoped = TRUE
		  AND ($2 = '' OR o.id::text = $2)
		ORDER BY u.display_name
	`, groupID, orgIDFilter)
	if err != nil {
		return nil, fmt.Errorf("repository.ListCrossOrgMemberships: %w", err)
	}
	defer rows.Close()

	list := make([]CrossOrgMembership, 0)
	for rows.Next() {
		var m CrossOrgMembership
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Role, &m.OrgID, &m.OrgName, &m.ProjectID, &m.ProjectName); err != nil {
			return nil, fmt.Errorf("repository.ListCrossOrgMemberships: scan: %w", err)
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListCrossOrgMemberships: %w", err)
	}
	return list, nil
}

// RevokeAllScopedForUser menghapus SELURUH keanggotaan project-scoped
// (is_scoped = TRUE) milik userID (S3-26) -- dipanggil dari service, TAPI
// BELUM DIPASANG ke endpoint manapun: task asli "terpasang pada endpoint
// deactivate" mengasumsikan fitur "nonaktifkan akun user" yang TIDAK ADA
// di aplikasi ini sampai sekarang (cuma organizations/workspaces yang
// punya deactivate, bukan users). Lihat implementation_gaps.md IG-18.
// Method ini siap dipanggil begitu fitur itu dibangun.
//
// ⚠️ RLS pm_delete membatasi hasil nyata ke row yang actor (session RLS
// exec) BERWENANG hapus -- kalau target punya scoped membership di grup
// LAIN yang tidak dikelola actor, row itu TIDAK ikut terhapus (RLS
// menyaring diam-diam, bukan error). Dampaknya dibahas di komentar IG-18.
func (r *ProjectMemberRepository) RevokeAllScopedForUser(ctx context.Context, exec db.Executor, userID string) (int64, error) {
	tag, err := exec.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1 AND is_scoped = TRUE`, userID)
	if err != nil {
		return 0, fmt.Errorf("repository.RevokeAllScopedForUser: %w", err)
	}
	return tag.RowsAffected(), nil
}

// insertProjectMemberAudit -- entity_id = target userID (siapa yang
// dimutasi), sama pola insertWorkspaceAudit's AssignRole. project_id
// disimpan di metadata JSONB, bukan kolom entity_id/workspace_id/org_id
// dedicated -- audit_logs tidak punya kolom project_id (§5.27).
func insertProjectMemberAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, projectID, targetUserID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, metadata)
		VALUES ($1, $2, $3, 'project_member', $4, jsonb_build_object('project_id', $5::uuid))
	`, actorID, actorRole, action, targetUserID, projectID)
	return err
}
