// Package repository -- ProjectRepository (S4-01/02/03, US-012). Tabel
// `projects` sudah ada sejak forward-pull S3 H9 (project_members), kolom
// code/pm_user_id/deleted_at/purge_scheduled_at ditambahkan
// 20260909090000_projects_code_pm_softdelete sesuai desain asli
// "AW Add Project.dc.html"/"AW Projects.dc.html" (dikonfirmasi user
// 2026-08-30). Kena RLS projects_select/_insert/_update sejak
// 20260829100000_rls_projects -- terima db.Executor per-panggilan, pola
// sama ProjectMemberRepository.
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

type ProjectRepository struct{}

func NewProjectRepository() *ProjectRepository {
	return &ProjectRepository{}
}

// Project -- satu baris hasil Create/Get/List (AW Projects.dc.html).
type Project struct {
	ID          string
	WorkspaceID string
	Name        string
	Code        string
	PMUserID    *string
	PMName      string
	PMEmail     string
	IsArchived  bool
	MemberCount int
	CreatedAt   time.Time
	ArchivedAt  *time.Time
	DeletedAt   *time.Time
}

// GetWorkspaceID mengembalikan workspace_id pemilik projectID -- dasar
// resolve otorisasi PUT/DELETE/archive (route /projects/:id tidak punya
// :wsId), sama pola ProjectMemberRepository.GetWorkspaceID. Sengaja TIDAK
// difilter deleted_at IS NULL -- dipakai juga oleh Restore.
func (r *ProjectRepository) GetWorkspaceID(ctx context.Context, exec db.Executor, projectID string) (string, error) {
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

// Create menyimpan project baru + audit trail (S4-02). code dan pm_user_id
// wajib diisi CALLER (service) -- divalidasi di sana, bukan di sini.
func (r *ProjectRepository) Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, actorID, actorRole string) (*Project, error) {
	p := &Project{WorkspaceID: workspaceID, Name: name, Code: code, PMUserID: &pmUserID}
	err := exec.QueryRow(ctx, `
		INSERT INTO projects (workspace_id, name, code, pm_user_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, workspaceID, name, code, pmUserID, actorID).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", classifyUniqueViolation(err, domain.ErrProjectCodeTaken))
	}

	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "project.created", p.ID, workspaceID, nil, nil,
		map[string]any{"name": name, "code": code}); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}
	return p, nil
}

// List mengembalikan project dalam satu workspace, TIDAK termasuk yang
// soft-deleted (AW Projects.dc.html: project terhapus hilang dari daftar
// sepenuhnya, beda dari arsip yang tetap tampil di tab "Arsip"). Scoping
// tambahan lewat RLS projects_select.
func (r *ProjectRepository) List(ctx context.Context, exec db.Executor, workspaceID string) ([]Project, error) {
	rows, err := exec.Query(ctx, `
		SELECT p.id, p.workspace_id, p.name, p.code, p.pm_user_id,
		       COALESCE(u.display_name, ''), COALESCE(u.email, ''),
		       p.is_archived, p.created_at, p.archived_at,
		       (SELECT COUNT(*) FROM project_members pm WHERE pm.project_id = p.id)
		FROM projects p
		LEFT JOIN users u ON u.id = p.pm_user_id
		WHERE p.workspace_id = $1 AND p.deleted_at IS NULL
		ORDER BY p.created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	list := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Code, &p.PMUserID,
			&p.PMName, &p.PMEmail, &p.IsArchived, &p.CreatedAt, &p.ArchivedAt, &p.MemberCount); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	return list, nil
}

// Update mengubah nama dan/atau PM penanggung jawab (S4-02). pmUserID
// kosong berarti PM tidak diubah (AW Projects.dc.html: reassignment cuma
// terjadi kalau pengguna benar-benar memilih orang lain).
func (r *ProjectRepository) Update(ctx context.Context, exec db.Executor, projectID, name, pmUserID, actorID, actorRole string) error {
	var oldName, oldPM string
	if err := exec.QueryRow(ctx, `
		SELECT name, COALESCE(pm_user_id::text, '') FROM projects WHERE id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&oldName, &oldPM); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.Update: %w", domain.ErrProjectNotFound)
		}
		return fmt.Errorf("repository.Update: %w", err)
	}

	newPM := pmUserID
	if newPM == "" {
		newPM = oldPM
	}
	tag, err := exec.Exec(ctx, `
		UPDATE projects SET name = $2, pm_user_id = $3, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, projectID, name, newPM)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", classifyUniqueViolation(err, domain.ErrProjectCodeTaken))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrProjectNotFound)
	}

	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", err)
	}
	before := map[string]any{"name": oldName, "pm_user_id": oldPM}
	after := map[string]any{"name": name, "pm_user_id": newPM}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "project.updated", projectID, workspaceID, before, after, nil); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// SetArchived mengarsipkan/batal-arsip project (S4-03). Project arsip
// read-only untuk member (AC US-012), rule level project dihentikan --
// ditegakkan di layer lain (task/rule service, di luar scope S4-01/02/03).
func (r *ProjectRepository) SetArchived(ctx context.Context, exec db.Executor, projectID string, archive bool, actorID, actorRole string) error {
	action := "project.unarchived"
	var tag pgconn.CommandTag
	var err error
	if archive {
		action = "project.archived"
		tag, err = exec.Exec(ctx, `
			UPDATE projects SET is_archived = TRUE, archived_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL AND is_archived = FALSE
		`, projectID)
	} else {
		tag, err = exec.Exec(ctx, `
			UPDATE projects SET is_archived = FALSE, archived_at = NULL, updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL AND is_archived = TRUE
		`, projectID)
	}
	if err != nil {
		return fmt.Errorf("repository.SetArchived: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetArchived: %w", domain.ErrProjectNotFound)
	}

	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.SetArchived: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, action, projectID, workspaceID, nil, nil, nil); err != nil {
		return fmt.Errorf("repository.SetArchived: audit: %w", err)
	}
	return nil
}

// SoftDelete menandai project dihapus (S4-02) -- BUKAN hard-delete seperti
// WorkspaceRepository.Delete: desain asli (AW Projects.dc.html) bilang
// task/sprint di dalamnya "dipindahkan ke jadwal penghapusan" dan "masih
// dapat dipulihkan Group Admin selama tenggat berjalan". purge_scheduled_at
// dihitung dari organizations.retention_days (§5.7) lewat organisasi
// pemilik workspace ini -- job purge otomatis belum dibangun (gap
// didokumentasikan, sama pola organizations.purge_scheduled_at).
func (r *ProjectRepository) SoftDelete(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE projects p
		SET deleted_at = NOW(),
		    purge_scheduled_at = NOW() + (
		      SELECT (o.retention_days || ' days')::interval
		      FROM workspaces w JOIN organizations o ON o.id = w.org_id
		      WHERE w.id = p.workspace_id
		    ),
		    updated_at = NOW()
		WHERE p.id = $1 AND p.deleted_at IS NULL
	`, projectID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrProjectNotFound)
	}

	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "project.deleted", projectID, workspaceID, nil, nil, nil); err != nil {
		return fmt.Errorf("repository.SoftDelete: audit: %w", err)
	}
	return nil
}

// Restore membatalkan soft-delete (Group Admin/Platform Admin saja --
// digerbangi di service, bukan di sini) selama masih dalam masa retensi.
func (r *ProjectRepository) Restore(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE projects SET deleted_at = NULL, purge_scheduled_at = NULL, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NOT NULL
	`, projectID)
	if err != nil {
		return fmt.Errorf("repository.Restore: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Restore: %w", domain.ErrProjectNotDeleted)
	}

	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.Restore: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "project.restored", projectID, workspaceID, nil, nil, nil); err != nil {
		return fmt.Errorf("repository.Restore: audit: %w", err)
	}
	return nil
}

// insertProjectAudit -- entity_id = projectID, workspace_id kolom dedicated
// (beda dari insertProjectMemberAudit yang simpan project_id di metadata
// karena entity_id-nya di sana adalah target user, bukan project),
// state_before/state_after untuk perubahan skalar (pola audit trail
// IG-29: snapshot immutable, bukan live JOIN).
func insertProjectAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, projectID, workspaceID string, stateBefore, stateAfter, metadata map[string]any) error {
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertProjectAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertProjectAudit: encode state_after: %w", err)
	}
	metaJSON, err := marshalIfNotEmpty(metadata)
	if err != nil {
		return fmt.Errorf("insertProjectAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'project', $4, $5, $6, $7, $8)
	`, actorID, actorRole, action, projectID, workspaceID, beforeJSON, afterJSON, metaJSON)
	return err
}
