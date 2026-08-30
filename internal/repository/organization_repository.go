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
	Domain            string
	DefaultLanguage   string
	StorageQuotaBytes int64
	StorageMaxBytes   int64
	StorageUsedBytes  int64
	RetentionDays     int
	WorkspaceCount    int
	MemberCount       int
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

// Update mengubah name/slug/domain organisasi. `domain` (email resmi) --
// S4G-02, Track S4G -- ditambahkan menyusul kolom `domain` (lihat migrasi
// 20260910090000), wording asli S3-03 ("nama/logo/domain") sengaja cuma
// mengerjakan nama/slug waktu itu karena kolomnya belum ada. `logo` TETAP
// di luar scope (belum ada storage file organisasi). domain kosong ("")
// disimpan sebagai NULL (opsional, sama pola description project).
func (r *OrganizationRepository) Update(ctx context.Context, exec db.Executor, orgID, name, slug, orgDomain, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET name = $2, slug = $3, domain = NULLIF($4, ''), updated_at = NOW()
		WHERE id = $1
	`, orgID, name, slug, orgDomain)
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

// UpdateStorageQuota mengubah storage_quota_bytes + retention_days
// organisasi (S3-34/US-011, retention ditambah S4G-03/Track S4G) --
// digabung satu method/satu endpoint karena desain "GA Organizations.dc.html"
// mengelompokkan keduanya dalam satu section "ALOKASI KUOTA STORAGE" dengan
// satu tombol simpan. Kuota divalidasi TIDAK melebihi storage_max_bytes
// (batas dari Platform Admin, glossary.md "Storage Quota"). CHECK constraint
// ck_org_storage_quota_within_max di DB adalah jaring pengaman kedua;
// validasi di sini yang memberi pesan error jelas (bukan constraint
// violation mentah). retentionDays divalidasi 30-365, sama batas dengan
// CHECK constraint ck_organizations_retention_days (§5.7) -- kolom ini
// sudah ada sejak awal tapi belum pernah ditulis kode manapun.
func (r *OrganizationRepository) UpdateStorageQuota(ctx context.Context, exec db.Executor, orgID string, quotaBytes int64, retentionDays int, actorID, actorRole string) error {
	if retentionDays < 30 || retentionDays > 365 {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrRetentionOutOfRange)
	}

	var maxBytes, usedMB int64
	var groupID string
	if err := exec.QueryRow(ctx, `SELECT storage_max_bytes, group_id, storage_used_mb FROM organizations WHERE id = $1`, orgID).Scan(&maxBytes, &groupID, &usedMB); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrOrganizationNotFound)
		}
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}
	if quotaBytes > maxBytes {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrStorageQuotaExceedsMax)
	}
	// S4G-02, Track S4G (desain "GA Organizations.dc.html"): kuota tidak
	// boleh diturunkan di bawah storage yang SUDAH terpakai -- kalau tidak,
	// organisasi langsung "over quota" begitu disimpan, memblokir seluruh
	// upload tanpa peringatan eksplisit ke GA saat submit.
	if usedBytes := usedMB * 1024 * 1024; quotaBytes < usedBytes {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrStorageQuotaBelowUsed)
	}

	// S4P-12: tegakkan plafon groups.storage_quota_gb (fallback ke
	// service_tiers.max_storage_gb kalau grup belum override manual) sebagai
	// ceiling GABUNGAN seluruh organisasi dalam grup itu -- sebelumnya cuma
	// disimpan/ditampilkan di form Group Admin (S4P-06/07), belum ditegakkan.
	groupCeilingGB, err := r.groupStorageCeilingGB(ctx, exec, groupID)
	if err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}
	var otherOrgsBytes int64
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(sum(storage_quota_bytes), 0) FROM organizations WHERE group_id = $1 AND id != $2
	`, groupID, orgID).Scan(&otherOrgsBytes); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: cek kuota organisasi lain: %w", err)
	}
	groupCeilingBytes := int64(groupCeilingGB) * 1024 * 1024 * 1024
	if groupCeilingGB > 0 && otherOrgsBytes+quotaBytes > groupCeilingBytes {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrGroupStorageQuotaExceedsCeiling)
	}

	if _, err := exec.Exec(ctx, `
		UPDATE organizations SET storage_quota_bytes = $2, retention_days = $3, updated_at = NOW()
		WHERE id = $1
	`, orgID, quotaBytes, retentionDays); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.storage_quota_updated", orgID); err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: audit: %w", err)
	}
	return nil
}

// groupStorageCeilingGB mengembalikan plafon storage grup (GB) --
// groups.storage_quota_gb (override manual), fallback ke
// service_tiers.max_storage_gb kalau grup belum override. Dipisah dari
// UpdateStorageQuota (S4G-03, Track S4G) supaya bisa di-reuse List untuk
// stat "KUOTA TERALOKASI / plafon" (desain "GA Organizations.dc.html") --
// TANPA endpoint/tabel baru, murni reuse lookup yang sudah ada.
func (r *OrganizationRepository) groupStorageCeilingGB(ctx context.Context, exec db.Executor, groupID string) (int, error) {
	var ceilingGB int
	if err := exec.QueryRow(ctx, `
		SELECT COALESCE(g.storage_quota_gb, st.max_storage_gb, 0)
		FROM groups g
		LEFT JOIN service_tiers st ON st.id = g.tier_id
		WHERE g.id = $1
	`, groupID).Scan(&ceilingGB); err != nil {
		return 0, fmt.Errorf("cek plafon grup: %w", err)
	}
	return ceilingGB, nil
}

// IsActive mengecek apakah organisasi TIDAK sedang nonaktif (S4G-04, Track
// S4G) -- dipakai WorkspaceService.MoveWorkspace sebagai guard org tujuan
// pindah workspace.
func (r *OrganizationRepository) IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error) {
	var deactivatedAt *time.Time
	err := exec.QueryRow(ctx, `SELECT deactivated_at FROM organizations WHERE id = $1`, orgID).Scan(&deactivatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("repository.IsActive: %w", domain.ErrOrganizationNotFound)
		}
		return false, fmt.Errorf("repository.IsActive: %w", err)
	}
	return deactivatedAt == nil, nil
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
// List mengembalikan organisasi yang terlihat oleh actor, plus plafon
// storage grup (GB, dikonversi bytes) untuk stat "KUOTA TERALOKASI /
// plafon" (S4G-03, Track S4G, desain "GA Organizations.dc.html") --
// ceilingBytes 0 kalau org kosong ATAU baris yang terlihat berasal dari
// LEBIH dari satu grup (kasus Platform Admin yang lihat semua organisasi
// lintas grup -- "satu plafon" tidak bermakna di situ, GA yang biasanya
// lihat halaman ini SELALU discoped RLS ke satu grup saja).
func (r *OrganizationRepository) List(ctx context.Context, exec db.Executor) ([]Organization, int64, error) {
	// workspace_count/member_count (S4G-03, Track S4G, desain
	// "GA Organizations.dc.html" kolom "WS · MEMBER") -- dihitung per baris
	// lewat subquery correlated, sama pola GetSummary, TAPI di sini
	// multi-row -- reuse yang sama supaya tidak N+1 request GetSummary per
	// organisasi dari FE.
	rows, err := exec.Query(ctx, `
		SELECT o.id, o.group_id, o.name, o.slug, COALESCE(o.domain, ''), o.default_language,
		       o.storage_quota_bytes, o.storage_max_bytes, o.storage_used_mb * 1024 * 1024, o.retention_days,
		       COALESCE((SELECT COUNT(*) FROM workspaces w WHERE w.org_id = o.id AND w.archived_at IS NULL), 0),
		       COALESCE((SELECT COUNT(DISTINCT wm.user_id) FROM workspace_members wm
		                 JOIN workspaces w2 ON w2.id = wm.workspace_id WHERE w2.org_id = o.id), 0),
		       o.deactivated_at, o.created_at
		FROM organizations o
		ORDER BY o.name
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	orgs := make([]Organization, 0)
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.GroupID, &o.Name, &o.Slug, &o.Domain, &o.DefaultLanguage, &o.StorageQuotaBytes, &o.StorageMaxBytes, &o.StorageUsedBytes, &o.RetentionDays, &o.WorkspaceCount, &o.MemberCount, &o.DeactivatedAt, &o.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.List: scan: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.List: %w", err)
	}

	var ceilingBytes int64
	if len(orgs) > 0 {
		sameGroup := true
		for i := range orgs {
			if orgs[i].GroupID != orgs[0].GroupID {
				sameGroup = false
				break
			}
		}
		if sameGroup {
			ceilingGB, err := r.groupStorageCeilingGB(ctx, exec, orgs[0].GroupID)
			if err != nil {
				return nil, 0, fmt.Errorf("repository.List: %w", err)
			}
			ceilingBytes = int64(ceilingGB) * 1024 * 1024 * 1024
		}
	}
	return orgs, ceilingBytes, nil
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
