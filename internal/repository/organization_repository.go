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

// OrganizationRepository tidak menyimpan *pgxpool.Pool -- setiap method
// menerima db.Executor sebagai parameter (S3-42, direfactor dari pool
// langsung begitu organizations/workspaces benar-benar di-RLS -- pola sama
// dengan refactor WorkspaceMemberRepository di S2-11). Executor adalah
// transaksi request-scoped dari middleware.DBContextMiddleware yang sudah
// membawa session variable RLS (app.current_user_id/app.current_platform_role).
type OrganizationRepository struct{}

func NewOrganizationRepository() *OrganizationRepository {
	return &OrganizationRepository{}
}

// Organization -- subset kolom DATABASE_SCHEMA.md §5.7 yang dipakai response
// S3-02/03/04, ditambah default_language/storage_quota_bytes/
// storage_max_bytes (S3-29/32).
type Organization struct {
	ID                string
	GroupID           string
	Name              string
	Slug              string
	DefaultLanguage   string
	StorageQuotaBytes int64
	StorageMaxBytes   int64
	DeactivatedAt     *time.Time
	CreatedAt         time.Time
}

// IsGroupAdminOfGroup mengecek apakah userID adalah salah satu GA yang
// di-assign ke groupID (group_admin_assignments, S3-38) -- dasar otorisasi
// scoped Group Admin di S3-02/03/04 (implementation_gaps.md IG-01).
func (r *OrganizationRepository) IsGroupAdminOfGroup(ctx context.Context, exec db.Executor, userID, groupID string) (bool, error) {
	var exists bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_admin_assignments WHERE user_id = $1 AND group_id = $2)
	`, userID, groupID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repository.IsGroupAdminOfGroup: %w", err)
	}
	return exists, nil
}

// GetGroupID mengembalikan group_id pemilik orgID -- dipakai service
// meresolve grup mana yang harus dicek IsGroupAdminOfGroup saat Update/
// Deactivate (beda dari Create yang group_id-nya datang dari request body).
func (r *OrganizationRepository) GetGroupID(ctx context.Context, exec db.Executor, orgID string) (string, error) {
	var groupID string
	err := exec.QueryRow(ctx, `SELECT group_id FROM organizations WHERE id = $1`, orgID).Scan(&groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.GetGroupID: %w", domain.ErrOrganizationNotFound)
		}
		return "", fmt.Errorf("repository.GetGroupID: %w", err)
	}
	return groupID, nil
}

// Create menyimpan organisasi baru + audit trail. Atomicity dijamin
// transaksi request-scoped yang dibawa exec (S3-42, pola sama S2-11) --
// bukan transaksi lokal di sini lagi.
func (r *OrganizationRepository) Create(ctx context.Context, exec db.Executor, groupID, name, slug, actorID, actorRole string) (*Organization, error) {
	org := &Organization{GroupID: groupID, Name: name, Slug: slug}
	err := exec.QueryRow(ctx, `
		INSERT INTO organizations (group_id, name, slug)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`, groupID, name, slug).Scan(&org.ID, &org.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.created", org.ID); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}
	return org, nil
}

// Update mengubah name/slug organisasi -- DATABASE_SCHEMA.md §5.7 tidak
// punya kolom logo/domain seperti wording asli S3-03 di sprint_backlog.md,
// lihat catatan di sana.
func (r *OrganizationRepository) Update(ctx context.Context, exec db.Executor, orgID, name, slug, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET name = $2, slug = $3, updated_at = NOW()
		WHERE id = $1
	`, orgID, name, slug)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.updated", orgID); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// UpdateSettings mengubah default_language organisasi (S3-30, US-010).
func (r *OrganizationRepository) UpdateSettings(ctx context.Context, exec db.Executor, orgID, defaultLanguage, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET default_language = $2::org_language, updated_at = NOW()
		WHERE id = $1
	`, orgID, defaultLanguage)
	if err != nil {
		return fmt.Errorf("repository.UpdateSettings: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateSettings: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.settings_updated", orgID); err != nil {
		return fmt.Errorf("repository.UpdateSettings: audit: %w", err)
	}
	return nil
}

// UpdateStorageQuota mengubah storage_quota_bytes organisasi (S3-34,
// US-011) -- divalidasi TIDAK melebihi storage_max_bytes (batas dari
// Platform Admin, glossary.md "Storage Quota"). CHECK constraint
// ck_org_storage_quota_within_max di DB adalah jaring pengaman kedua;
// validasi di sini yang memberi pesan error jelas (bukan constraint
// violation mentah).
func (r *OrganizationRepository) UpdateStorageQuota(ctx context.Context, exec db.Executor, orgID string, quotaBytes int64, actorID, actorRole string) error {
	var maxBytes int64
	var groupID string
	if err := exec.QueryRow(ctx, `SELECT storage_max_bytes, group_id FROM organizations WHERE id = $1`, orgID).Scan(&maxBytes, &groupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrOrganizationNotFound)
		}
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}
	if quotaBytes > maxBytes {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrStorageQuotaExceedsMax)
	}

	// S4P-12: tegakkan plafon groups.storage_quota_gb (fallback ke
	// service_tiers.max_storage_gb kalau grup belum override manual) sebagai
	// ceiling GABUNGAN seluruh organisasi dalam grup itu -- sebelumnya cuma
	// disimpan/ditampilkan di form Group Admin (S4P-06/07), belum ditegakkan.
	var groupCeilingGB int
	var otherOrgsBytes int64
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(g.storage_quota_gb, st.max_storage_gb, 0),
		       COALESCE((SELECT sum(o.storage_quota_bytes) FROM organizations o
		                 WHERE o.group_id = g.id AND o.id != $2), 0)
		FROM groups g
		LEFT JOIN service_tiers st ON st.name = g.tier
		WHERE g.id = $1
	`, groupID, orgID).Scan(&groupCeilingGB, &otherOrgsBytes); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: cek plafon grup: %w", err)
	}
	groupCeilingBytes := int64(groupCeilingGB) * 1024 * 1024 * 1024
	if groupCeilingGB > 0 && otherOrgsBytes+quotaBytes > groupCeilingBytes {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrGroupStorageQuotaExceedsCeiling)
	}

	if _, err := exec.Exec(ctx, `
		UPDATE organizations SET storage_quota_bytes = $2, updated_at = NOW()
		WHERE id = $1
	`, orgID, quotaBytes); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.storage_quota_updated", orgID); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: audit: %w", err)
	}
	return nil
}

// Deactivate menyetel deactivated_at (US-007 AC: akses member diblokir,
// data tetap tersimpan -- soft, bukan DELETE). Idempotent secara struktur
// (mengizinkan re-deactivate) TIDAK divalidasi di sini -- service yang
// menolak kalau perlu; repository murni menulis.
func (r *OrganizationRepository) Deactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET deactivated_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deactivated_at IS NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Deactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Deactivate: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.deactivated", orgID); err != nil {
		return fmt.Errorf("repository.Deactivate: audit: %w", err)
	}
	return nil
}

// Reactivate mengosongkan deactivated_at (kebalikan Deactivate) -- bukan
// task tersendiri di sprint_backlog.md (S3-04 cuma sebut deactivate), tapi
// prasyarat langsung S3-07 (FE): panel kelola organisasi (GA Organizations.dc.html)
// selalu punya toggle dua arah AKTIF<->NONAKTIF, sama pola S3-11 workspaces
// yang MEMANG sudah sepasang deactivate+reactivate sejak awal. Ditemukan
// lewat implementasi FE, dicatat sebagai IG-09-style forward-pull minimal.
func (r *OrganizationRepository) Reactivate(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET deactivated_at = NULL, updated_at = NOW()
		WHERE id = $1 AND deactivated_at IS NOT NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Reactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Reactivate: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.reactivated", orgID); err != nil {
		return fmt.Errorf("repository.Reactivate: audit: %w", err)
	}
	return nil
}

// List mengembalikan organisasi yang TERLIHAT oleh actor (S3-07 prasyarat,
// sama pola IG-09). Tidak ada filter WHERE eksplisit di sini -- scoping
// (Platform Admin lihat semua, Group Admin cuma org dalam grup yang dia
// kelola, member cuma org sendiri) SEPENUHNYA ditegakkan RLS `orgs_select`
// (S3-42) lewat exec yang session variable-nya sudah disuntik
// DBContextMiddleware -- query di sini polos SELECT *.
func (r *OrganizationRepository) List(ctx context.Context, exec db.Executor) ([]Organization, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, group_id, name, slug, default_language, storage_quota_bytes, storage_max_bytes, deactivated_at, created_at
		FROM organizations
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	orgs := make([]Organization, 0)
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.GroupID, &o.Name, &o.Slug, &o.DefaultLanguage, &o.StorageQuotaBytes, &o.StorageMaxBytes, &o.DeactivatedAt, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.List: scan: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.List: %w", err)
	}
	return orgs, nil
}

// Delete menghapus organisasi permanen (S3-05, US-007 AC) -- HANYA kalau
// tidak ada workspace AKTIF (archived_at IS NULL) di dalamnya. Workspace
// yang sudah diarsipkan tidak menghalangi -- AC "semua workspace sudah
// dihapus/dipindahkan" diartikan sebagai "tidak ada lagi yang aktif",
// konsisten dengan `workspaces` yang soft-delete (archived_at), bukan
// hard-delete (§5.9).
func (r *OrganizationRepository) Delete(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	var hasActiveWorkspaces bool
	if err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM workspaces WHERE org_id = $1 AND archived_at IS NULL)
	`, orgID).Scan(&hasActiveWorkspaces); err != nil {
		return fmt.Errorf("repository.Delete: cek workspace aktif: %w", err)
	}
	if hasActiveWorkspaces {
		return fmt.Errorf("repository.Delete: %w", domain.ErrOrganizationHasWorkspaces)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.deleted", orgID); err != nil {
		return fmt.Errorf("repository.Delete: audit: %w", err)
	}

	tag, err := exec.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Delete: %w", domain.ErrOrganizationNotFound)
	}
	return nil
}

// Summary -- S3-06, dashboard ringkasan GA.
type Summary struct {
	MemberCount     int
	WorkspaceCount  int
	StorageUsedByte int64
}

// GetSummary mengembalikan agregat organisasi (S3-06): total member unik
// lintas seluruh workspace di organisasi ini, total workspace AKTIF, dan
// storage usage. `storage_used_mb` (§5.7, sudah ada sejak organisasi
// dibuat) dikonversi ke bytes -- kolom `storage_quota_bytes`/
// `storage_used_bytes` yang lebih presisi baru ditambahkan S3-32 (US-011),
// belum ada sekarang.
func (r *OrganizationRepository) GetSummary(ctx context.Context, exec db.Executor, orgID string) (*Summary, error) {
	var s Summary
	var storageUsedMB int64
	// FROM organizations o WHERE o.id = $1 sebagai anchor -- kalau org tidak
	// ada, query ini mengembalikan NOL baris (bukan satu baris dengan
	// agregat 0/NULL) sehingga pgx.ErrNoRows benar-benar ter-trigger.
	err := exec.QueryRow(ctx, `
		SELECT
			o.storage_used_mb,
			COALESCE((SELECT COUNT(*) FROM workspaces WHERE org_id = o.id AND archived_at IS NULL), 0),
			COALESCE((SELECT COUNT(DISTINCT wm.user_id)
				FROM workspace_members wm
				JOIN workspaces w ON w.id = wm.workspace_id
				WHERE w.org_id = o.id), 0)
		FROM organizations o
		WHERE o.id = $1
	`, orgID).Scan(&storageUsedMB, &s.WorkspaceCount, &s.MemberCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.GetSummary: %w", domain.ErrOrganizationNotFound)
		}
		return nil, fmt.Errorf("repository.GetSummary: %w", err)
	}
	s.StorageUsedByte = storageUsedMB * 1024 * 1024
	return &s, nil
}

func insertOrgAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, orgID string) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id)
		VALUES ($1, $2, $3, 'organization', $4, $4)
	`, actorID, actorRole, action, orgID)
	return err
}
