package repository

import (
	"context"
	"encoding/json"
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
	Domains           []string
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

// OrganizationDomain -- satu baris domain email resmi organisasi (S4G-02
// semula kolom tunggal `organizations.domain`, dipecah jadi tabel
// one-to-many 2026-09-11 -- satu organisasi bisa punya lebih dari satu
// domain, dikonfirmasi user). Unik PER-ORGANISASI saja (bukan global).
type OrganizationDomain struct {
	ID             string
	OrganizationID string
	Domain         string
	CreatedAt      time.Time
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
// bukan transaksi lokal di sini lagi. domain/defaultLanguage/quotaBytes/
// retentionDays ditambahkan S4G-31 (Track S4G, desain
// "GA Add Organization.dc.html") -- sebelumnya Create cuma menyimpan
// group_id/name/slug, memaksa GA mengisi kuota/retensi/bahasa/domain lewat
// panggilan Update terpisah setelah organisasi dibuat (tidak sesuai alur
// desain, satu form sekali submit). Kuota/retensi divalidasi lewat
// UpdateStorageQuota yang sudah ada (reuse penuh: cek storage_max_bytes,
// plafon storage grup gabungan) -- kalau validasi gagal, INSERT ini ikut
// roll back bersama transaksi request-scoped.
func (r *OrganizationRepository) Create(ctx context.Context, exec db.Executor, groupID, name, slug, orgDomain, defaultLanguage string, quotaBytes int64, retentionDays int, actorID, actorRole string) (*Organization, error) {
	org := &Organization{GroupID: groupID, Name: name, Slug: slug, DefaultLanguage: defaultLanguage}
	err := exec.QueryRow(ctx, `
		INSERT INTO organizations (group_id, name, slug, default_language)
		VALUES ($1, $2, $3, $4::org_language)
		RETURNING id, created_at
	`, groupID, name, slug, defaultLanguage).Scan(&org.ID, &org.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("repository.Create: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.created", org.ID, nil, nil); err != nil {
		return nil, fmt.Errorf("repository.Create: audit: %w", err)
	}

	// domain (S4G-02) diisi opsional saat Create -- kalau ada, jadi baris
	// pertama organization_domains (2026-09-11: kolom tunggal dipecah jadi
	// tabel one-to-many, lihat OrganizationDomain). GA bisa tambah domain
	// lain lagi kapan saja lewat AddDomain.
	if orgDomain != "" {
		if _, err := r.insertDomain(ctx, exec, org.ID, orgDomain, actorID, actorRole); err != nil {
			return nil, fmt.Errorf("repository.Create: %w", err)
		}
		org.Domains = []string{orgDomain}
	}

	if err := r.UpdateStorageQuota(ctx, exec, org.ID, quotaBytes, retentionDays, actorID, actorRole); err != nil {
		return nil, fmt.Errorf("repository.Create: %w", err)
	}
	org.StorageQuotaBytes = quotaBytes
	org.RetentionDays = retentionDays

	return org, nil
}

// Update mengubah name/slug organisasi (S3-03). Domain email resmi
// (S4G-02) DIPISAH dari sini sejak 2026-09-11 -- lihat AddDomain/
// RemoveDomain, organisasi sekarang bisa punya lebih dari satu domain,
// tidak lagi cocok sebagai satu field dalam form nama/slug.
func (r *OrganizationRepository) Update(ctx context.Context, exec db.Executor, orgID, name, slug, actorID, actorRole string) error {
	// Nilai lama diambil DULU (2026-09-12, ditemukan user: audit trail
	// "Organisasi diperbarui" tidak pernah menampilkan NILAI SEBELUM/SESUDAH
	// -- insertOrgAudit sebelumnya tidak menerima state_before/state_after
	// sama sekali, beda dari insertProjectAudit yang sudah punya pola ini)
	// supaya bisa dibandingkan ke nilai baru untuk audit trail.
	var oldName, oldSlug string
	if err := exec.QueryRow(ctx, `SELECT name, slug FROM organizations WHERE id = $1 AND deleted_at IS NULL`, orgID).Scan(&oldName, &oldSlug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.Update: %w", domain.ErrOrganizationNotFound)
		}
		return fmt.Errorf("repository.Update: %w", err)
	}

	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET name = $2, slug = $3, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, orgID, name, slug)
	if err != nil {
		return fmt.Errorf("repository.Update: %w", classifyUniqueViolation(err, domain.ErrSlugAlreadyExists))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Update: %w", domain.ErrOrganizationNotFound)
	}

	before := map[string]any{"name": oldName, "slug": oldSlug}
	after := map[string]any{"name": name, "slug": slug}
	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.updated", orgID, before, after); err != nil {
		return fmt.Errorf("repository.Update: audit: %w", err)
	}
	return nil
}

// insertDomain menyimpan satu baris organization_domains + audit trail --
// dipakai Create (domain awal opsional) dan AddDomain (GA menambah domain
// lain kapan saja lewat ManageOrganizationModal).
func (r *OrganizationRepository) insertDomain(ctx context.Context, exec db.Executor, orgID, domainValue, actorID, actorRole string) (string, error) {
	var id string
	err := exec.QueryRow(ctx, `
		INSERT INTO organization_domains (organization_id, domain) VALUES ($1, $2) RETURNING id
	`, orgID, domainValue).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insertDomain: %w", classifyUniqueViolation(err, domain.ErrOrganizationDomainExists))
	}
	if err := insertOrgDomainAudit(ctx, exec, actorID, actorRole, "organization.domain_added", id, orgID, domainValue); err != nil {
		return "", fmt.Errorf("insertDomain: audit: %w", err)
	}
	return id, nil
}

// ListDomains mengembalikan seluruh domain email resmi organisasi (id +
// domain), diurutkan created_at -- dipakai FE ManageOrganizationModal untuk
// menampilkan chip domain YANG BISA DIHAPUS (baris List() organizations
// biasa cuma mengembalikan array string domain, tanpa id, cukup untuk
// tampilan ringkas tapi tidak cukup untuk tombol hapus per-domain).
func (r *OrganizationRepository) ListDomains(ctx context.Context, exec db.Executor, orgID string) ([]OrganizationDomain, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, organization_id, domain, created_at FROM organization_domains
		WHERE organization_id = $1 ORDER BY created_at
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListDomains: %w", err)
	}
	defer rows.Close()

	domains := make([]OrganizationDomain, 0)
	for rows.Next() {
		var d OrganizationDomain
		if err := rows.Scan(&d.ID, &d.OrganizationID, &d.Domain, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListDomains: scan: %w", err)
		}
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListDomains: %w", err)
	}
	return domains, nil
}

// AddDomain menambah satu domain email resmi ke organisasi yang sudah ada
// (2026-09-11, dikonfirmasi user: satu organisasi bisa punya >1 domain).
func (r *OrganizationRepository) AddDomain(ctx context.Context, exec db.Executor, orgID, domainValue, actorID, actorRole string) (*OrganizationDomain, error) {
	id, err := r.insertDomain(ctx, exec, orgID, domainValue, actorID, actorRole)
	if err != nil {
		return nil, fmt.Errorf("repository.AddDomain: %w", err)
	}
	return &OrganizationDomain{ID: id, OrganizationID: orgID, Domain: domainValue}, nil
}

// RemoveDomain menghapus satu domain email resmi organisasi. `RETURNING
// domain` (bukan cek RowsAffected biasa) supaya nilai domain yang dihapus
// bisa dicatat di metadata audit (entity_id -- organization_domains.id --
// tidak resolve ke nama apa pun lewat JOIN, sama pola user_invitations).
func (r *OrganizationRepository) RemoveDomain(ctx context.Context, exec db.Executor, orgID, domainID, actorID, actorRole string) error {
	var domainValue string
	err := exec.QueryRow(ctx, `
		DELETE FROM organization_domains WHERE id = $1 AND organization_id = $2 RETURNING domain
	`, domainID, orgID).Scan(&domainValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.RemoveDomain: %w", domain.ErrOrganizationDomainNotFound)
		}
		return fmt.Errorf("repository.RemoveDomain: %w", err)
	}

	if err := insertOrgDomainAudit(ctx, exec, actorID, actorRole, "organization.domain_removed", domainID, orgID, domainValue); err != nil {
		return fmt.Errorf("repository.RemoveDomain: audit: %w", err)
	}
	return nil
}

// insertOrgDomainAudit -- chokepoint audit untuk domain_added/domain_removed.
// entity_type 'organization_domain' (BUKAN 'organization') karena entity_id
// menunjuk baris organization_domains, bukan organizations -- domain
// disimpan di metadata (satu-satunya cara mengidentifikasi baris ini,
// sama pola email di insertInvitationAudit). actor_ip ditangkap dari
// context, konsisten dengan konvensi audit trail yang sudah berlaku.
func insertOrgDomainAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, domainID, orgID, domainValue string) error {
	ip, path := requestMetaFromContext(ctx)
	metadata := map[string]any{"domain": domainValue}
	if path != "" {
		metadata["request_path"] = path
	}
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("insertOrgDomainAudit: encode metadata: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id, actor_ip, metadata)
		VALUES ($1, $2, $3, 'organization_domain', $4, $5, $6::inet, $7)
	`, actorID, actorRole, action, domainID, orgID, ip, metaJSON)
	return err
}

// UpdateSettings mengubah default_language organisasi (S3-30, US-010).
func (r *OrganizationRepository) UpdateSettings(ctx context.Context, exec db.Executor, orgID, defaultLanguage, actorID, actorRole string) error {
	var oldLanguage string
	if err := exec.QueryRow(ctx, `SELECT default_language FROM organizations WHERE id = $1 AND deleted_at IS NULL`, orgID).Scan(&oldLanguage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.UpdateSettings: %w", domain.ErrOrganizationNotFound)
		}
		return fmt.Errorf("repository.UpdateSettings: %w", err)
	}

	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET default_language = $2::org_language, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, orgID, defaultLanguage)
	if err != nil {
		return fmt.Errorf("repository.UpdateSettings: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateSettings: %w", domain.ErrOrganizationNotFound)
	}

	before := map[string]any{"default_language": oldLanguage}
	after := map[string]any{"default_language": defaultLanguage}
	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.settings_updated", orgID, before, after); err != nil {
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
// violation mentah). retentionDays divalidasi terhadap plafon TIER grup
// (S4G-34, Track S4G -- sebelumnya angka tetap 30-365 untuk semua tier,
// lihat groupRetentionRange) -- CHECK constraint
// ck_organizations_retention_days (§5.7, batas keras 30-365) tetap jadi
// jaring pengaman kedua.
func (r *OrganizationRepository) UpdateStorageQuota(ctx context.Context, exec db.Executor, orgID string, quotaBytes int64, retentionDays int, actorID, actorRole string) error {
	var maxBytes, usedMB, oldQuotaBytes int64
	var oldRetentionDays int
	var groupID string
	if err := exec.QueryRow(ctx, `
		SELECT storage_max_bytes, group_id, storage_used_mb, storage_quota_bytes, retention_days
		FROM organizations WHERE id = $1 AND deleted_at IS NULL
	`, orgID).Scan(&maxBytes, &groupID, &usedMB, &oldQuotaBytes, &oldRetentionDays); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.UpdateStorageQuota: %w", domain.ErrOrganizationNotFound)
		}
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}

	minDays, maxDays, tierName, err := r.groupRetentionRange(ctx, exec, groupID)
	if err != nil {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", err)
	}
	if retentionDays < minDays || retentionDays > maxDays {
		return fmt.Errorf("repository.UpdateStorageQuota: %w", &domain.RetentionOutOfRangeError{MinDays: minDays, MaxDays: maxDays, TierName: tierName})
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

	before := map[string]any{"storage_quota_bytes": oldQuotaBytes, "retention_days": oldRetentionDays}
	after := map[string]any{"storage_quota_bytes": quotaBytes, "retention_days": retentionDays}
	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.storage_quota_updated", orgID, before, after); err != nil {
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

// groupRetentionRange mengembalikan plafon retensi (hari) TIER grup --
// service_tiers.min_retention_days/max_retention_days (S4G-34, Track
// S4G), sebelumnya kolom ini cuma dipakai tampilan TierFactsPanel (FE),
// tidak pernah ditegakkan sungguhan (retensi organisasi selalu dicek
// terhadap angka tetap 30-365 untuk SEMUA tier). Di-clamp ke [30,365] --
// batas keras CHECK constraint ck_organizations_retention_days (§5.7)
// yang tidak bisa dilewati tier mana pun, jaga-jaga kalau data tier
// dikonfigurasi di luar rentang itu. Dipisah dari UpdateStorageQuota
// (pola sama groupStorageCeilingGB) supaya bisa direuse Create.
// GroupRetentionRange -- wrapper exported dari groupRetentionRange, dipakai
// OrganizationService.BulkUpdateRetentionPolicy (Data Retention, modal
// "Atur Kebijakan") untuk validasi SEKALI di depan sebelum menulis banyak
// organisasi sekaligus (rentang sama untuk seluruh grup, bukan per-org).
func (r *OrganizationRepository) GroupRetentionRange(ctx context.Context, exec db.Executor, groupID string) (minDays, maxDays int, tierName string, err error) {
	return r.groupRetentionRange(ctx, exec, groupID)
}

func (r *OrganizationRepository) groupRetentionRange(ctx context.Context, exec db.Executor, groupID string) (minDays, maxDays int, tierName string, err error) {
	if err := exec.QueryRow(ctx, `
		SELECT GREATEST(30, COALESCE(st.min_retention_days, 30)),
		       LEAST(365, COALESCE(st.max_retention_days, 365)),
		       COALESCE(st.name, '-')
		FROM groups g
		LEFT JOIN service_tiers st ON st.id = g.tier_id
		WHERE g.id = $1
	`, groupID).Scan(&minDays, &maxDays, &tierName); err != nil {
		return 0, 0, "", fmt.Errorf("cek plafon retensi grup: %w", err)
	}
	return minDays, maxDays, tierName, nil
}

// IsActive mengecek apakah organisasi TIDAK sedang nonaktif (S4G-04, Track
// S4G) -- dipakai WorkspaceService.MoveWorkspace sebagai guard org tujuan
// pindah workspace.
func (r *OrganizationRepository) IsActive(ctx context.Context, exec db.Executor, orgID string) (bool, error) {
	var deactivatedAt *time.Time
	err := exec.QueryRow(ctx, `SELECT deactivated_at FROM organizations WHERE id = $1 AND deleted_at IS NULL`, orgID).Scan(&deactivatedAt)
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
		WHERE id = $1 AND deleted_at IS NULL AND deactivated_at IS NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Deactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Deactivate: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.deactivated", orgID, nil, nil); err != nil {
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
		WHERE id = $1 AND deleted_at IS NULL AND deactivated_at IS NOT NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Reactivate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Reactivate: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.reactivated", orgID, nil, nil); err != nil {
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
// groupID kosong berarti TIDAK difilter -- perilaku lama, RLS-only,
// dipakai Platform Admin (lintas grup) dan actor yang belum menentukan
// grup aktif. groupID diisi (S4G-32, Track S4G, group switcher) berarti
// scoping tambahan di LEVEL APLIKASI -- RLS `orgs_select` cuma menjamin
// "grup APA SAJA yang actor kelola", tidak ada konsep "grup yang sedang
// aktif dipilih" (dibutuhkan begitu satu Group Admin bisa mengelola >1
// grup, DATABASE_SCHEMA.md §5.6 many-to-many); validasi actor benar-benar
// berwenang atas groupID ini tetap di service (authorizeGroup), di sini
// murni filter WHERE.
func (r *OrganizationRepository) List(ctx context.Context, exec db.Executor, groupID string) ([]Organization, int64, error) {
	// workspace_count/member_count (S4G-03, Track S4G, desain
	// "GA Organizations.dc.html" kolom "WS · MEMBER") -- dihitung per baris
	// lewat subquery correlated, sama pola GetSummary, TAPI di sini
	// multi-row -- reuse yang sama supaya tidak N+1 request GetSummary per
	// organisasi dari FE.
	query := `
		SELECT o.id, o.group_id, o.name, o.slug,
		       COALESCE((SELECT array_agg(od.domain ORDER BY od.created_at) FROM organization_domains od WHERE od.organization_id = o.id), ARRAY[]::text[]),
		       o.default_language,
		       o.storage_quota_bytes, o.storage_max_bytes, o.storage_used_mb * 1024 * 1024, o.retention_days,
		       COALESCE((SELECT COUNT(*) FROM workspaces w WHERE w.org_id = o.id AND w.archived_at IS NULL), 0),
		       COALESCE((SELECT COUNT(DISTINCT wm.user_id) FROM workspace_members wm
		                 JOIN workspaces w2 ON w2.id = wm.workspace_id WHERE w2.org_id = o.id), 0),
		       o.deactivated_at, o.created_at
		FROM organizations o
	`
	args := []any{}
	if groupID != "" {
		query += ` WHERE o.group_id = $1 AND o.deleted_at IS NULL`
		args = append(args, groupID)
	} else {
		query += ` WHERE o.deleted_at IS NULL`
	}
	query += ` ORDER BY o.name`

	rows, err := exec.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.List: %w", err)
	}
	defer rows.Close()

	orgs := make([]Organization, 0)
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.GroupID, &o.Name, &o.Slug, &o.Domains, &o.DefaultLanguage, &o.StorageQuotaBytes, &o.StorageMaxBytes, &o.StorageUsedBytes, &o.RetentionDays, &o.WorkspaceCount, &o.MemberCount, &o.DeactivatedAt, &o.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.List: scan: %w", err)
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.List: %w", err)
	}

	var ceilingBytes int64
	switch {
	case groupID != "":
		// groupID eksplisit (query param tervalidasi service) -- ceiling
		// selalu well-defined langsung dari groupID, tidak perlu nebak dari
		// orgs[0] (jadi juga benar untuk grup yang MEMANG kosong, 0
		// organisasi -- bukan cuma "sameGroup" heuristik lama).
		ceilingGB, err := r.groupStorageCeilingGB(ctx, exec, groupID)
		if err != nil {
			return nil, 0, fmt.Errorf("repository.List: %w", err)
		}
		ceilingBytes = int64(ceilingGB) * 1024 * 1024 * 1024
	case len(orgs) > 0:
		// groupID tidak diberikan (mis. Platform Admin lintas grup) --
		// heuristik lama: ceiling cuma bermakna kalau kebetulan SELURUH
		// baris yang RLS izinkan berasal dari satu grup yang sama.
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

// SoftDelete memindahkan organisasi ke jadwal penghapusan (2026-09-12,
// sebelumnya hard DELETE -- ditemukan user via pengujian live role Group
// Admin: "hard delete diganti dengan soft delete, persis seperti pada
// penghapusan workspace"). HANYA kalau tidak ada workspace AKTIF
// (archived_at IS NULL) di dalamnya -- guard lama dipertahankan apa
// adanya. Workspace yang sudah diarsipkan tidak menghalangi -- AC "semua
// workspace sudah dihapus/dipindahkan" diartikan sebagai "tidak ada lagi
// yang aktif", konsisten dengan `workspaces` yang soft-delete
// (archived_at), bukan hard-delete (§5.9).
//
// purge_scheduled_at dihitung dari retention_days MILIK organisasi itu
// sendiri (EDITABLE lewat UpdateStorageQuota) -- BEDA dari
// orgDeactivationRetentionDays (retensi TETAP 90 hari, kebijakan platform
// untuk Deactivate, lihat RetentionRepository) -- deleted_at dan
// deactivated_at ORTHOGONAL, sama pola workspaces.
func (r *OrganizationRepository) SoftDelete(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	var hasActiveWorkspaces bool
	if err := exec.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM workspaces WHERE org_id = $1 AND archived_at IS NULL)
	`, orgID).Scan(&hasActiveWorkspaces); err != nil {
		return fmt.Errorf("repository.SoftDelete: cek workspace aktif: %w", err)
	}
	if hasActiveWorkspaces {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrOrganizationHasWorkspaces)
	}

	tag, err := exec.Exec(ctx, `
		UPDATE organizations
		SET deleted_at = NOW(),
		    purge_scheduled_at = NOW() + (retention_days || ' days')::interval,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.SoftDelete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SoftDelete: %w", domain.ErrOrganizationNotFound)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.deleted", orgID, nil, nil); err != nil {
		return fmt.Errorf("repository.SoftDelete: audit: %w", err)
	}
	return nil
}

// Restore membatalkan soft-delete (kebalikan SoftDelete) -- otorisasi sama
// persis (Platform Admin/Group Admin pengelola grup pemilik org), lihat
// OrganizationService.RestoreOrganization.
func (r *OrganizationRepository) Restore(ctx context.Context, exec db.Executor, orgID, actorID, actorRole string) error {
	tag, err := exec.Exec(ctx, `
		UPDATE organizations SET deleted_at = NULL, purge_scheduled_at = NULL, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NOT NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("repository.Restore: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.Restore: %w", domain.ErrOrganizationNotDeleted)
	}

	if err := insertOrgAudit(ctx, exec, actorID, actorRole, "organization.restored", orgID, nil, nil); err != nil {
		return fmt.Errorf("repository.Restore: audit: %w", err)
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

// insertOrgAudit -- stateBefore/stateAfter (2026-09-12, ditemukan user: GA
// Audit Trail tidak pernah menampilkan NILAI SEBELUM/SESUDAH untuk aksi
// organisasi apa pun) mengikuti pola insertProjectAudit -- nil untuk aksi
// yang namanya sudah cukup menjelaskan diri sendiri (created/deactivated/
// reactivated/deleted/restored), diisi untuk aksi "diperbarui" yang
// nilainya benar-benar berubah (Update/UpdateSettings/UpdateStorageQuota).
// actor_ip + metadata.request_path (2026-09-12, ditemukan user lewat
// pertanyaan yang sama: kolom ASAL di GA Audit Trail kosong untuk SEMUA
// aksi organisasi) -- insertOrgAudit sebelumnya TIDAK PERNAH memanggil
// requestMetaFromContext sama sekali, beda dari insertOrgDomainAudit
// (helper audit organisasi LAIN, dipakai add/remove domain) yang sudah
// benar sejak awal.
func insertOrgAudit(ctx context.Context, exec db.Executor, actorID, actorRole, action, orgID string, stateBefore, stateAfter map[string]any) error {
	ip, path := requestMetaFromContext(ctx)
	var metaJSON []byte
	if path != "" {
		encoded, err := marshalIfNotEmpty(map[string]any{"request_path": path})
		if err != nil {
			return fmt.Errorf("insertOrgAudit: encode metadata: %w", err)
		}
		metaJSON = encoded
	}
	beforeJSON, err := marshalIfNotEmpty(stateBefore)
	if err != nil {
		return fmt.Errorf("insertOrgAudit: encode state_before: %w", err)
	}
	afterJSON, err := marshalIfNotEmpty(stateAfter)
	if err != nil {
		return fmt.Errorf("insertOrgAudit: encode state_after: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, org_id, actor_ip, state_before, state_after, metadata)
		VALUES ($1, $2, $3, 'organization', $4, $4, $5::inet, $6, $7, $8)
	`, actorID, actorRole, action, orgID, ip, beforeJSON, afterJSON, metaJSON)
	return err
}
