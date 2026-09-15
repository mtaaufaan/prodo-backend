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
// CreatedByName/CreatedByEmail (dikonfirmasi user 2026-09-13) -- workspace
// bisa punya lebih dari satu admin_workspace, jadi perlu jelas project mana
// dibuat oleh siapa; kolom `created_by` sendiri sudah terisi sejak Create
// (S4-02), cuma belum pernah di-JOIN/ditampilkan.
type Project struct {
	ID             string
	WorkspaceID    string
	Name           string
	Code           string
	PMUserID       *string
	PMName         string
	PMEmail        string
	IsArchived     bool
	MemberCount    int
	SprintCount    int
	TaskCount      int
	CreatedByName  string
	CreatedByEmail string
	// PMPendingEmail/PMPendingInvitationID -- kosong kecuali PMUserID nil DAN
	// ada undangan project_manager pending tertaut project ini ("menunggu
	// PM", S4W susulan). InvitationID dipakai FE untuk tombol "Cabut" lewat
	// endpoint cancel-invitation yang SUDAH ADA (sama dipakai undangan
	// admin_workspace), bukan endpoint baru.
	PMPendingEmail        string
	PMPendingInvitationID string
	CreatedAt             time.Time
	ArchivedAt            *time.Time
	DeletedAt             *time.Time
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

// PMProjectRef -- satu baris hasil ListPMProjectNames.
type PMProjectRef struct {
	ID   string
	Name string
}

// ListPMProjectNames mengembalikan project di workspaceID ini yang
// pm_user_id-nya userID -- dipakai RBACService.AssignRole (Kelola Member &
// Roles, S4W susulan role restructuring 2026-09-14) sebagai guard:
// mengubah role SEORANG PM ke role lain tidak boleh menyisakan project
// manapun tanpa PM, AW harus tetapkan PM baru dulu lewat Kelola Project.
func (r *ProjectRepository) ListPMProjectNames(ctx context.Context, exec db.Executor, workspaceID, userID string) ([]PMProjectRef, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, name FROM projects WHERE workspace_id = $1 AND pm_user_id = $2 AND deleted_at IS NULL
	`, workspaceID, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPMProjectNames: %w", err)
	}
	defer rows.Close()

	var refs []PMProjectRef
	for rows.Next() {
		var ref PMProjectRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, fmt.Errorf("repository.ListPMProjectNames: scan: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListPMProjectNames: rows: %w", err)
	}
	return refs, nil
}

// GetAllowEditorStoryPoints -- Phase 4 (US-018a/S4-56): gate izin Editor
// mengisi story point, dikonfigurasi per project (default FALSE, PM-only).
func (r *ProjectRepository) GetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string) (bool, error) {
	var allow bool
	err := exec.QueryRow(ctx, `SELECT allow_editor_story_points FROM projects WHERE id = $1`, projectID).Scan(&allow)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("repository.GetAllowEditorStoryPoints: %w", domain.ErrProjectNotFound)
		}
		return false, fmt.Errorf("repository.GetAllowEditorStoryPoints: %w", err)
	}
	return allow, nil
}

// SetAllowEditorStoryPoints -- PUT /projects/:id/settings (Phase 4).
func (r *ProjectRepository) SetAllowEditorStoryPoints(ctx context.Context, exec db.Executor, projectID string, allow bool) error {
	tag, err := exec.Exec(ctx, `UPDATE projects SET allow_editor_story_points = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, projectID, allow)
	if err != nil {
		return fmt.Errorf("repository.SetAllowEditorStoryPoints: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SetAllowEditorStoryPoints: %w", domain.ErrProjectNotFound)
	}
	return nil
}

// Create menyimpan project baru + audit trail (S4-02). code wajib diisi
// CALLER (service) -- divalidasi di sana, bukan di sini. pmUserID BOLEH
// kosong (S4W susulan, dikonfirmasi user 2026-09-13) -- project masuk
// status "menunggu PM" (pm_user_id NULL) kalau AW memilih undang PM baru
// lewat email yang belum terdaftar; caller (service) yang menautkan
// undangan project_manager ke project ini SETELAH baris ini dibuat (perlu
// project.ID lebih dulu).
func (r *ProjectRepository) Create(ctx context.Context, exec db.Executor, workspaceID, name, code, pmUserID, actorID, actorRole string) (*Project, error) {
	p := &Project{WorkspaceID: workspaceID, Name: name, Code: code}
	var pmParam any
	if pmUserID != "" {
		pmParam = pmUserID
		p.PMUserID = &pmUserID
	}
	err := exec.QueryRow(ctx, `
		INSERT INTO projects (workspace_id, name, code, pm_user_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, workspaceID, name, code, pmParam, actorID).Scan(&p.ID, &p.CreatedAt)
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
		       (SELECT COUNT(*) FROM project_members pm WHERE pm.project_id = p.id),
		       (SELECT COUNT(*) FROM sprints s WHERE s.project_id = p.id),
		       (SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id AND t.deleted_at IS NULL),
		       COALESCE(creator.display_name, ''), COALESCE(creator.email, ''),
		       COALESCE(pm_pending.email, ''), COALESCE(pm_pending.id::text, '')
		FROM projects p
		LEFT JOIN users u ON u.id = p.pm_user_id
		LEFT JOIN users creator ON creator.id = p.created_by
		LEFT JOIN LATERAL (
			SELECT id, email FROM user_invitations
			WHERE project_id = p.id AND accepted_at IS NULL AND cancelled_at IS NULL
			ORDER BY created_at DESC LIMIT 1
		) pm_pending ON true
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
			&p.PMName, &p.PMEmail, &p.IsArchived, &p.CreatedAt, &p.ArchivedAt, &p.MemberCount,
			&p.SprintCount, &p.TaskCount, &p.CreatedByName, &p.CreatedByEmail,
			&p.PMPendingEmail, &p.PMPendingInvitationID); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	return list, nil
}

// NameExists mengecek apakah nama project (case-insensitive) sudah dipakai
// project lain DI WORKSPACE yang sama -- S4W-03, "AW Add Project.dc.html"
// (`taken = projects.some(p => p.name.toLowerCase() === name.toLowerCase())`).
// excludeProjectID kosong berarti tidak ada pengecualian (Create); diisi
// projectID sendiri saat Update supaya project itu sendiri tidak dianggap
// bentrok dengan namanya sendiri.
func (r *ProjectRepository) NameExists(ctx context.Context, exec db.Executor, workspaceID, name, excludeProjectID string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM projects
			WHERE workspace_id = $1 AND deleted_at IS NULL AND lower(name) = lower($2) AND id::text != $3
		)
	`, workspaceID, name, excludeProjectID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.NameExists: %w", err)
	}
	return exists, nil
}

// AssignPendingPM (S4W susulan) -- dipanggil InvitationService.AcceptInvitation
// begitu undangan project_manager yang tertaut project TERTENTU (project_id
// di user_invitations, migrasi 20261017090000) diterima. WHERE
// pm_user_id IS NULL adalah guard idempotensi -- kalau project sudah keburu
// dapat PM lain lewat jalur berbeda (jarang), UPDATE ini jadi no-op (BUKAN
// error) alih-alih menimpa PM yang sudah benar.
func (r *ProjectRepository) AssignPendingPM(ctx context.Context, exec db.Executor, projectID, userID string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE projects SET pm_user_id = $2, updated_at = NOW() WHERE id = $1 AND pm_user_id IS NULL
	`, projectID, userID)
	if err != nil {
		return fmt.Errorf("repository.AssignPendingPM: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.AssignPendingPM: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, userID, "member", "project.pm_assigned", projectID, workspaceID, nil,
		map[string]any{"pm_user_id": userID}, nil); err != nil {
		return fmt.Errorf("repository.AssignPendingPM: audit: %w", err)
	}
	return nil
}

// RemovePM (S4W susulan) mengosongkan pm_user_id -- project masuk status
// "menunggu PM" sampai PM baru ditetapkan/undangan baru diterima. BEDA dari
// Update yang menganggap pmUserID kosong sebagai "tidak diubah" -- ini aksi
// eksplisit terpisah dipicu tombol "Hapus PM" panel Kelola, dikonfirmasi
// user boleh dilakukan kapan saja (bukan cuma saat undangan pending).
func (r *ProjectRepository) RemovePM(ctx context.Context, exec db.Executor, projectID, actorID, actorRole string) error {
	var oldPM string
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(pm_user_id::text, '') FROM projects WHERE id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&oldPM); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.RemovePM: %w", domain.ErrProjectNotFound)
		}
		return fmt.Errorf("repository.RemovePM: %w", err)
	}
	if _, err := exec.Exec(ctx, `UPDATE projects SET pm_user_id = NULL, updated_at = NOW() WHERE id = $1`, projectID); err != nil {
		return fmt.Errorf("repository.RemovePM: %w", err)
	}
	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.RemovePM: %w", err)
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, "project.pm_removed", projectID, workspaceID,
		map[string]any{"pm_user_id": oldPM}, nil, nil); err != nil {
		return fmt.Errorf("repository.RemovePM: audit: %w", err)
	}
	return nil
}

// NotifyPMRemoved (susulan 2026-09-15, dikonfirmasi user "jangan lupa
// mengeluarkan notifikasi sesuai standar sebelumnya") -- in-app
// notification ke user yang kehilangan status PM SATU project (dipanggil
// RBACService.RemoveMember, kasus member itu PM di LEBIH dari satu
// project -- lihat komentar di sana). RemovePM sendiri SENGAJA tidak
// diubah supaya tombol "Hapus PM" existing di Kelola Project (gap
// terpisah, di luar cakupan) tidak ikut berubah perilakunya tanpa
// diminta. Pola sama AssignRole/AddMember -- insert langsung, tidak ada
// NotificationRepository terpisah di codebase ini.
func (r *ProjectRepository) NotifyPMRemoved(ctx context.Context, exec db.Executor, projectID, userID, actorID string) error {
	var name string
	if err := exec.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&name); err != nil {
		return fmt.Errorf("repository.NotifyPMRemoved: %w", err)
	}
	title := "Tidak Lagi Jadi Project Manager"
	body := fmt.Sprintf("Anda tidak lagi menjadi Project Manager untuk project %s.", name)
	if _, err := exec.Exec(ctx, `
		INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
		VALUES ($1, $2, 'project_pm_removed', 'project', $3, $4, $5)
	`, userID, actorID, projectID, title, body); err != nil {
		return fmt.Errorf("repository.NotifyPMRemoved: %w", err)
	}
	return nil
}

// SetPM (S4W susulan) menetapkan pm_user_id TANPA syarat (beda dari
// AssignPendingPM yang cuma jalan kalau sebelumnya NULL) -- dipakai
// AssignPM saat AW eksplisit menetapkan/mengganti PM lewat panel Kelola,
// baik project sedang "menunggu PM" maupun sudah punya PM aktif lain.
func (r *ProjectRepository) SetPM(ctx context.Context, exec db.Executor, projectID, userID, actorID, actorRole string) error {
	var oldPM string
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(pm_user_id::text, '') FROM projects WHERE id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&oldPM); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.SetPM: %w", domain.ErrProjectNotFound)
		}
		return fmt.Errorf("repository.SetPM: %w", err)
	}
	if _, err := exec.Exec(ctx, `UPDATE projects SET pm_user_id = $2, updated_at = NOW() WHERE id = $1`, projectID, userID); err != nil {
		return fmt.Errorf("repository.SetPM: %w", err)
	}
	workspaceID, err := r.GetWorkspaceID(ctx, exec, projectID)
	if err != nil {
		return fmt.Errorf("repository.SetPM: %w", err)
	}
	action := "project.pm_assigned"
	var before map[string]any
	if oldPM != "" {
		action = "project.pm_reassigned"
		before = map[string]any{"pm_user_id": oldPM}
	}
	if err := insertProjectAudit(ctx, exec, actorID, actorRole, action, projectID, workspaceID, before,
		map[string]any{"pm_user_id": userID}, nil); err != nil {
		return fmt.Errorf("repository.SetPM: audit: %w", err)
	}
	return nil
}

// GetPendingPMInvitationID -- ID undangan project_manager pending yang
// tertaut project ini kalau ada, "" kalau tidak ada. Dipakai service.AssignPM
// untuk auto-cancel undangan lama SEBELUM membuat undangan PM baru (satu
// project cuma boleh punya SATU undangan PM pending sekaligus).
func (r *ProjectRepository) GetPendingPMInvitationID(ctx context.Context, exec db.Executor, projectID string) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		SELECT id FROM user_invitations
		WHERE project_id = $1 AND accepted_at IS NULL AND cancelled_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("repository.GetPendingPMInvitationID: %w", err)
	}
	return id, nil
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
// IG-29: snapshot immutable, bukan live JOIN). actor_ip/metadata.request_path
// (implementation_gaps.md IG-64) ditambahkan 2026-09-12 -- sebelumnya
// TIDAK PERNAH diisi walau state_before/after sudah benar sejak awal.
func insertProjectAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, projectID, workspaceID string, stateBefore, stateAfter, metadata map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	if path != "" {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["request_path"] = path
	}
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
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, workspace_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'project', $4, $5, $6::inet, $7, $8, $9)
	`, actorID, actorRole, action, projectID, workspaceID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}
