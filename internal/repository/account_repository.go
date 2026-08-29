package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mtaaufaan/prodo-backend/internal/domain"
)

// AccountRepository menyimpan data akun -- tabel platform-level inti (users,
// user_auth_providers, platform_invitations, audit_logs/platform_audit_logs)
// sengaja TIDAK di-RLS (docs/RLS_DESIGN.md §8: belum terikat org_id/
// workspace_id). TAPI beberapa query (groupAdminSummaryQuery, katalog
// audit S4P-22) JOIN ke tabel yang KENA RLS (organizations, workspace_members,
// platform_audit_logs sendiri) -- pemanggil query semacam itu WAJIB lewat
// withPlatformAdminRLS (rls_helpers.go), bukan r.db langsung, kalau tidak
// hasilnya diam-diam 0 baris (IG-24).
type AccountRepository struct {
	db *pgxpool.Pool
}

func NewAccountRepository(db *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{db: db}
}

// CreateGroupAdminInvitationParams membungkus seluruh data yang dibutuhkan
// untuk provisioning satu akun Group Admin. Semua insert (users,
// user_auth_providers, platform_invitations, groups,
// group_admin_assignments, audit_logs) terjadi dalam satu transaksi --
// all-or-nothing, lihat docs/DATABASE_SCHEMA.md §5.1/5.2/5.5/5.6/5.27
// dan migrations/20260820150000_users_auth_providers.up.sql +
// 20260820150100_platform_invitations_audit_logs.up.sql.
//
// GroupName -- IG-21: SETIAP GA baru WAJIB langsung mengelola satu grup
// (sesuai desain "Tambah Group Admin", field "Nama Perusahaan / Grup") --
// tidak ada lagi GA "yatim" tanpa grup seperti sejak S1-05. JobTitle/
// Address/Phone/Tier/StorageQuotaGB -- S4P-06/07, sesuai desain "PA
// Group Admin Form" -- field kontak & paket layanan grup, ditambahkan
// setelah forward-pull tier system (S4 H4 lanjutan). StorageQuotaGB nil
// berarti pakai plafon default tier.
type CreateGroupAdminInvitationParams struct {
	Email           string
	DisplayName     string
	GroupName       string
	JobTitle        string
	Address         string
	Phone           string
	TierID          string
	StorageQuotaGB  *int
	KeycloakUserID  string
	TokenHash       string
	ExpiresAt       time.Time
	InvitedByUserID string
}

// CreateGroupAdminInvitation menyimpan user baru (is_active=false,
// platform_role='group_admin'), referensi Keycloak-nya, token aktivasi,
// grup baru + assignment-nya (IG-21), dan entry audit trail (US-073 AC:
// "seluruh aksi onboarding dicatat"). Kalau email sudah ada, mengembalikan
// domain.ErrEmailAlreadyExists.
func (r *AccountRepository) CreateGroupAdminInvitation(ctx context.Context, p *CreateGroupAdminInvitationParams) (userID string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, platform_role, is_active)
		VALUES ($1, $2, 'group_admin', FALSE)
		RETURNING id
	`, p.Email, p.DisplayName).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", fmt.Errorf("repository.CreateGroupAdminInvitation: %w", domain.ErrEmailAlreadyExists)
		}
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert users: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO user_auth_providers (user_id, provider, provider_sub)
		VALUES ($1, 'local', $2)
	`, userID, p.KeycloakUserID); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert user_auth_providers: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO platform_invitations (email, platform_role, invited_by, token_hash, expires_at)
		VALUES ($1, 'group_admin', $2, $3, $4)
	`, p.Email, p.InvitedByUserID, p.TokenHash, p.ExpiresAt); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert platform_invitations: %w", err)
	}

	// S4P-11: tier harus ada dan masih assignable (belum nonaktif/archived)
	// -- dicek di sini, bukan cuma di service, supaya atomik dalam tx yang
	// sama dengan insert groups (menghindari race tier di-nonaktifkan PA
	// lain di antara validasi dan insert).
	var tierAssignable bool
	if err = tx.QueryRow(ctx, `
		SELECT deactivated_at IS NULL AND archived_at IS NULL FROM service_tiers WHERE id = $1
	`, p.TierID).Scan(&tierAssignable); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.CreateGroupAdminInvitation: %w", domain.ErrInvalidTier)
		}
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: cek tier: %w", err)
	}
	if !tierAssignable {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: %w", domain.ErrInvalidTier)
	}

	var groupID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO groups (name, tier_id, job_title, address, phone, storage_quota_gb)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, p.GroupName, p.TierID, p.JobTitle, p.Address, p.Phone, p.StorageQuotaGB).Scan(&groupID); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert groups: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO group_admin_assignments (group_id, user_id, assigned_by)
		VALUES ($1, $2, $3)
	`, groupID, userID, p.InvitedByUserID); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert group_admin_assignments: %w", err)
	}

	metadata, err := json.Marshal(map[string]string{
		"email":         p.Email,
		"platform_role": "group_admin",
		"group_id":      groupID,
	})
	if err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: encode metadata: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id, metadata)
		VALUES ($1, 'platform_admin', 'user.invited', 'user', $2, $3::jsonb)
	`, p.InvitedByUserID, userID, string(metadata)); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert platform_audit_logs: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: commit tx: %w", err)
	}
	return userID, nil
}

// GroupAdminSummary adalah satu baris daftar Group Admin untuk panel
// Platform Admin (S1-12, diperkaya S4P-06 sesuai desain "PA Group
// Admins" -- kolom TIER/SISA ORG/SISA KUOTA/SISA MEMBER/TANGGAL DAFTAR).
// Mengambil grup PERTAMA (assigned_at paling awal) milik GA -- simplifikasi
// yang disengaja: GA yang mengelola >1 grup (mis. setelah jadi target
// TransferGroup, S4P-03) cuma menampilkan grup pertamanya di sini, dicatat
// sebagai batasan yang diketahui di sprint_backlog.md, bukan bug.
type GroupAdminSummary struct {
	ID              string
	Email           string
	DisplayName     string
	IsActive        bool
	SuspendedAt     *time.Time
	CreatedAt       time.Time
	GroupID         *string
	GroupName       *string
	JobTitle        *string
	Address         *string
	Phone           *string
	TierID          *string
	Tier            *string
	TierMaxOrg      int
	TierMaxStorage  int
	TierMaxMembers  int
	UsedOrgCount    int
	UsedStorageMB   int64
	UsedMemberCount int
	StorageQuotaGB  *int
}

// groupAdminSummaryQuery -- SELECT bersama ListGroupAdmins (banyak baris)
// dan GetGroupAdminDetail (satu baris): grup pertama (LATERAL, assigned_at
// paling awal) + katalog tier + agregat pemakaian (jumlah organisasi,
// storage terpakai, jumlah member unik) di bawah grup itu.
const groupAdminSummaryQuery = `
	SELECT u.id, u.email, u.display_name, u.is_active, u.suspended_at, u.created_at,
	       g.id, g.name, g.job_title, g.address, g.phone, g.tier_id, st.name, g.storage_quota_gb,
	       COALESCE(st.max_org, 0), COALESCE(st.max_storage_gb, 0), COALESCE(st.max_members, 0),
	       COALESCE(org_agg.org_count, 0), COALESCE(org_agg.storage_used_mb, 0), COALESCE(mem_agg.member_count, 0)
	FROM users u
	LEFT JOIN LATERAL (
		SELECT gaa.group_id FROM group_admin_assignments gaa
		WHERE gaa.user_id = u.id ORDER BY gaa.assigned_at LIMIT 1
	) primary_group ON true
	LEFT JOIN groups g ON g.id = primary_group.group_id
	LEFT JOIN service_tiers st ON st.id = g.tier_id
	LEFT JOIN LATERAL (
		SELECT count(*) AS org_count, COALESCE(sum(o.storage_used_mb), 0) AS storage_used_mb
		FROM organizations o WHERE o.group_id = g.id
	) org_agg ON true
	LEFT JOIN LATERAL (
		SELECT count(DISTINCT wm.user_id) AS member_count
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN organizations o2 ON o2.id = w.org_id
		WHERE o2.group_id = g.id
	) mem_agg ON true
	WHERE u.platform_role = 'group_admin' AND u.deleted_at IS NULL
`

func scanGroupAdminSummary(row interface{ Scan(...any) error }) (GroupAdminSummary, error) {
	var s GroupAdminSummary
	err := row.Scan(&s.ID, &s.Email, &s.DisplayName, &s.IsActive, &s.SuspendedAt, &s.CreatedAt,
		&s.GroupID, &s.GroupName, &s.JobTitle, &s.Address, &s.Phone, &s.TierID, &s.Tier, &s.StorageQuotaGB,
		&s.TierMaxOrg, &s.TierMaxStorage, &s.TierMaxMembers,
		&s.UsedOrgCount, &s.UsedStorageMB, &s.UsedMemberCount)
	return s, err
}

// ListGroupAdmins mengembalikan seluruh user dengan platform_role='group_admin',
// terbaru dulu, dengan pagination sederhana (docs/coding-conventions.md §7.1).
func (r *AccountRepository) ListGroupAdmins(ctx context.Context, limit, offset int) ([]GroupAdminSummary, int, error) {
	var total int
	var result []GroupAdminSummary
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM users WHERE platform_role = 'group_admin' AND deleted_at IS NULL
		`).Scan(&total); err != nil {
			return fmt.Errorf("count: %w", err)
		}

		rows, err := tx.Query(ctx, groupAdminSummaryQuery+`
			ORDER BY u.created_at DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			s, err := scanGroupAdminSummary(rows)
			if err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			result = append(result, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, fmt.Errorf("repository.ListGroupAdmins: %w", err)
	}
	return result, total, nil
}

// GetGroupAdminDetail mengembalikan satu Group Admin + grup pertamanya
// untuk mode Lihat/Ubah (S4P-06). domain.ErrUserNotFound kalau tidak ada.
func (r *AccountRepository) GetGroupAdminDetail(ctx context.Context, targetUserID string) (*GroupAdminSummary, error) {
	var s GroupAdminSummary
	err := withPlatformAdminRLS(ctx, r.db, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, groupAdminSummaryQuery+` AND u.id = $1`, targetUserID)
		scanned, err := scanGroupAdminSummary(row)
		if err != nil {
			return err
		}
		s = scanned
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.GetGroupAdminDetail: %w", domain.ErrUserNotFound)
		}
		return nil, fmt.Errorf("repository.GetGroupAdminDetail: %w", err)
	}
	return &s, nil
}

// UpdateGroupAdminParams -- S4P-06, field form "Ubah Group Admin".
// NewStatus kosong berarti status tidak diubah; kalau diisi harus
// 'AKTIF' atau 'SUSPENDED' -- 'TIDAK AKTIF' (pending, belum aktivasi)
// TIDAK bisa diset manual lewat form ini (domain.ErrInvalidStatusTransition),
// hanya dicapai lewat alur onboarding yang belum selesai.
type UpdateGroupAdminParams struct {
	DisplayName    string
	GroupName      string
	JobTitle       string
	Address        string
	Phone          string
	TierID         string
	StorageQuotaGB *int
	NewStatus      string // "", "AKTIF", atau "SUSPENDED"
}

// UpdateGroupAdmin memperbarui data GA + grup pertamanya, dan status kalau
// diminta (S4P-06). Semua dalam satu transaksi + audit log. Mengembalikan
// tier SEBELUM update (S4P-09) supaya caller tahu apakah tier benar-benar
// berubah (notifikasi/email tier_changed cuma dikirim kalau berubah).
func (r *AccountRepository) UpdateGroupAdmin(ctx context.Context, targetUserID string, p *UpdateGroupAdminParams, actorUserID string) (oldTier string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var groupID, oldTierID string
	if err := tx.QueryRow(ctx, `
		SELECT gaa.group_id, g.tier_id, st.name FROM group_admin_assignments gaa
		JOIN groups g ON g.id = gaa.group_id
		LEFT JOIN service_tiers st ON st.id = g.tier_id
		WHERE gaa.user_id = $1 ORDER BY gaa.assigned_at LIMIT 1
	`, targetUserID).Scan(&groupID, &oldTierID, &oldTier); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrUserNotFound)
		}
		return "", fmt.Errorf("repository.UpdateGroupAdmin: cari grup: %w", err)
	}

	// S4P-11: tier cuma perlu assignable (belum nonaktif/archived) kalau
	// BENAR-BENAR berubah -- GA yang tier-nya belakangan dinonaktifkan
	// tetap boleh disimpan ulang selama field lain (bukan tier) yang diedit.
	var newTierName string
	if p.TierID != "" && p.TierID != oldTierID {
		var tierAssignable bool
		if err := tx.QueryRow(ctx, `
			SELECT name, deactivated_at IS NULL AND archived_at IS NULL FROM service_tiers WHERE id = $1
		`, p.TierID).Scan(&newTierName, &tierAssignable); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrInvalidTier)
			}
			return "", fmt.Errorf("repository.UpdateGroupAdmin: cek tier: %w", err)
		}
		if !tierAssignable {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrInvalidTier)
		}
	}

	if tag, err := tx.Exec(ctx, `
		UPDATE users SET display_name = $2, updated_at = NOW() WHERE id = $1 AND platform_role = 'group_admin'
	`, targetUserID, p.DisplayName); err != nil {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: update users: %w", err)
	} else if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrUserNotFound)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE groups SET name = $2, tier_id = $3, job_title = $4, address = $5, phone = $6,
		       storage_quota_gb = $7, updated_at = NOW()
		WHERE id = $1
	`, groupID, p.GroupName, p.TierID, p.JobTitle, p.Address, p.Phone, p.StorageQuotaGB); err != nil {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: update groups: %w", err)
	}

	// S4P-09: notifikasi in-app ke GA saat tier grupnya berubah -- pola
	// insert inline sama seperti project_member_repository.go/
	// workspace_member_repository.go, best-effort (bagian tx yang sama,
	// tapi kegagalan notifikasi bukan skenario yang divalidasi terpisah --
	// kalau tx gagal, seluruh update ikut rollback, konsisten).
	if p.TierID != "" && p.TierID != oldTierID {
		if _, err := tx.Exec(ctx, `
			INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
			VALUES ($1, $2, 'tier_changed', 'group', $3, 'Tier Grup Berubah', $4)
		`, targetUserID, actorUserID, groupID,
			fmt.Sprintf("Tier grup Anda diubah dari %s menjadi %s oleh Platform Admin.", oldTier, newTierName)); err != nil {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: insert notifikasi tier_changed: %w", err)
		}
	}

	switch p.NewStatus {
	case "AKTIF":
		// is_active = TRUE di WHERE -- "AKTIF" cuma valid sebagai
		// un-suspend (akun yang PERNAH aktif), BUKAN force-activate akun
		// yang masih pending (is_active masih FALSE, belum menyelesaikan
		// onboarding US-073). Tanpa guard ini, request "AKTIF" pada akun
		// pending jadi no-op diam-diam (suspended_at memang sudah NULL)
		// tapi status yang ditampilkan tetap "TIDAK AKTIF" -- ditemukan
		// lewat verifikasi live, bukan dugaan.
		tag, err := tx.Exec(ctx, `UPDATE users SET suspended_at = NULL WHERE id = $1 AND is_active = TRUE`, targetUserID)
		if err != nil {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: reactivate: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrInvalidStatusTransition)
		}
	case "SUSPENDED":
		if _, err := tx.Exec(ctx, `UPDATE users SET suspended_at = NOW() WHERE id = $1`, targetUserID); err != nil {
			return "", fmt.Errorf("repository.UpdateGroupAdmin: suspend: %w", err)
		}
	case "":
		// status tidak diubah
	default:
		return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", domain.ErrInvalidStatusTransition)
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.updated", targetUserID); err != nil {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.UpdateGroupAdmin: commit tx: %w", err)
	}
	return oldTier, nil
}

// ServiceTier -- satu baris katalog tier (S4P-07, diperluas S4P-11 dengan
// lifecycle nonaktif/archive + tier custom). ID adalah PK sejak S4P-11 --
// name berhenti jadi PK supaya rename tier tidak memutus referensi
// groups.tier_id yang sudah ada (lihat migration
// 20260903090000_service_tiers_lifecycle_and_uuid_pk).
type ServiceTier struct {
	ID               string
	Name             string
	MinRetentionDays int
	MaxRetentionDays int
	WebhookRate      int
	SSOEnabled       bool
	MaxOrg           int
	MaxStorageGB     int
	MaxMembers       int
	IsCustom         bool
	DeactivatedAt    *time.Time
	ArchivedAt       *time.Time
}

const serviceTierColumns = `id, name, min_retention_days, max_retention_days, webhook_rate, sso_enabled,
	       max_org, max_storage_gb, max_members, is_custom, deactivated_at, archived_at`

func scanServiceTier(row interface{ Scan(...any) error }) (ServiceTier, error) {
	var t ServiceTier
	err := row.Scan(&t.ID, &t.Name, &t.MinRetentionDays, &t.MaxRetentionDays, &t.WebhookRate, &t.SSOEnabled,
		&t.MaxOrg, &t.MaxStorageGB, &t.MaxMembers, &t.IsCustom, &t.DeactivatedAt, &t.ArchivedAt)
	return t, err
}

// ListServiceTiers mengembalikan katalog tier. includeArchived=false
// (dropdown assign tier ke GA) cuma mengembalikan tier yang masih
// assignable (belum nonaktif/archived); includeArchived=true (halaman admin
// "Tier & Kuota Global") mengembalikan SEMUA tier termasuk yang
// nonaktif/archived, supaya PA bisa memulihkannya. Urutan: tier standar
// dulu (starter < business < enterprise), baru tier custom (alfabet).
func (r *AccountRepository) ListServiceTiers(ctx context.Context, includeArchived bool) ([]ServiceTier, error) {
	query := `SELECT ` + serviceTierColumns + ` FROM service_tiers`
	if !includeArchived {
		query += ` WHERE deactivated_at IS NULL AND archived_at IS NULL`
	}
	query += ` ORDER BY is_custom, CASE name WHEN 'starter' THEN 1 WHEN 'business' THEN 2 WHEN 'enterprise' THEN 3 ELSE 4 END, name`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repository.ListServiceTiers: %w", err)
	}
	defer rows.Close()

	var result []ServiceTier
	for rows.Next() {
		t, err := scanServiceTier(rows)
		if err != nil {
			return nil, fmt.Errorf("repository.ListServiceTiers: scan: %w", err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListServiceTiers: rows: %w", err)
	}
	return result, nil
}

// FindActiveServiceTierIDByName -- dipakai CreateGroupAdmin untuk resolve
// default tier "starter" saat request tidak menyertakan tier_id (S4P-11).
func (r *AccountRepository) FindActiveServiceTierIDByName(ctx context.Context, name string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		SELECT id FROM service_tiers WHERE name = $1 AND deactivated_at IS NULL AND archived_at IS NULL
	`, name).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.FindActiveServiceTierIDByName: %w", domain.ErrTierNotFound)
		}
		return "", fmt.Errorf("repository.FindActiveServiceTierIDByName: %w", err)
	}
	return id, nil
}

// ServiceTierParams -- field yang bisa diisi PA lewat form Tambah/Kelola
// Tier (S4P-11).
type ServiceTierParams struct {
	Name             string
	MinRetentionDays int
	MaxRetentionDays int
	WebhookRate      int
	SSOEnabled       bool
	MaxOrg           int
	MaxStorageGB     int
	MaxMembers       int
}

// CreateServiceTier menambah tier CUSTOM baru ke katalog (is_custom=true --
// 3 tier standar sudah di-seed lewat migration, tidak dibuat lewat sini).
func (r *AccountRepository) CreateServiceTier(ctx context.Context, p *ServiceTierParams, actorUserID string) (id string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("repository.CreateServiceTier: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	err = tx.QueryRow(ctx, `
		INSERT INTO service_tiers (name, min_retention_days, max_retention_days, webhook_rate, sso_enabled, max_org, max_storage_gb, max_members, is_custom)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE)
		RETURNING id
	`, p.Name, p.MinRetentionDays, p.MaxRetentionDays, p.WebhookRate, p.SSOEnabled, p.MaxOrg, p.MaxStorageGB, p.MaxMembers).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", fmt.Errorf("repository.CreateServiceTier: %w", domain.ErrTierNameAlreadyExists)
		}
		return "", fmt.Errorf("repository.CreateServiceTier: insert: %w", err)
	}

	if err := logTierAudit(ctx, tx, actorUserID, "tier.created", id); err != nil {
		return "", fmt.Errorf("repository.CreateServiceTier: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.CreateServiceTier: commit tx: %w", err)
	}
	return id, nil
}

// UpdateServiceTier mengubah definisi tier (S4P-11) -- termasuk rename,
// aman sejak name berhenti jadi PK.
func (r *AccountRepository) UpdateServiceTier(ctx context.Context, id string, p *ServiceTierParams, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.UpdateServiceTier: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE service_tiers SET name = $2, min_retention_days = $3, max_retention_days = $4,
		       webhook_rate = $5, sso_enabled = $6, max_org = $7, max_storage_gb = $8, max_members = $9,
		       updated_at = NOW()
		WHERE id = $1
	`, id, p.Name, p.MinRetentionDays, p.MaxRetentionDays, p.WebhookRate, p.SSOEnabled, p.MaxOrg, p.MaxStorageGB, p.MaxMembers)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("repository.UpdateServiceTier: %w", domain.ErrTierNameAlreadyExists)
		}
		return fmt.Errorf("repository.UpdateServiceTier: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.UpdateServiceTier: %w", domain.ErrTierNotFound)
	}

	if err := logTierAudit(ctx, tx, actorUserID, "tier.updated", id); err != nil {
		return fmt.Errorf("repository.UpdateServiceTier: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.UpdateServiceTier: commit tx: %w", err)
	}
	return nil
}

// updateTierLifecycleColumn -- helper bersama 4 aksi lifecycle tier
// (nonaktifkan/aktifkan/archive/pulihkan, S4P-11): masing-masing sekadar
// toggle satu kolom timestamp + audit log. sqlStatement SELALU konstanta
// yang di-hardcode caller (lihat 4 fungsi di bawah) -- tidak pernah dari
// input pengguna.
func (r *AccountRepository) updateTierLifecycleColumn(ctx context.Context, id, actorUserID, auditAction, sqlStatement string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.%s: begin tx: %w", auditAction, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, sqlStatement, id)
	if err != nil {
		return fmt.Errorf("repository.%s: update: %w", auditAction, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.%s: %w", auditAction, domain.ErrTierNotFound)
	}
	if err := logTierAudit(ctx, tx, actorUserID, auditAction, id); err != nil {
		return fmt.Errorf("repository.%s: %w", auditAction, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.%s: commit tx: %w", auditAction, err)
	}
	return nil
}

// DeactivateServiceTier/ReactivateServiceTier -- toggle deactivated_at.
// Tier nonaktif tetap tampil di daftar utama tapi tidak bisa di-assign ke
// GA baru; GA yang sudah memakainya tidak terpengaruh.
func (r *AccountRepository) DeactivateServiceTier(ctx context.Context, id, actorUserID string) error {
	return r.updateTierLifecycleColumn(ctx, id, actorUserID, "tier.deactivated",
		`UPDATE service_tiers SET deactivated_at = NOW(), updated_at = NOW() WHERE id = $1`)
}

func (r *AccountRepository) ReactivateServiceTier(ctx context.Context, id, actorUserID string) error {
	return r.updateTierLifecycleColumn(ctx, id, actorUserID, "tier.reactivated",
		`UPDATE service_tiers SET deactivated_at = NULL, updated_at = NOW() WHERE id = $1`)
}

// ArchiveServiceTier/UnarchiveServiceTier -- toggle archived_at, independen
// dari deactivated_at. Tier archived disembunyikan dari daftar utama
// (perlu toggle "Tampilkan Arsip"), langkah administratif sebelum tier itu
// aman dihapus permanen.
func (r *AccountRepository) ArchiveServiceTier(ctx context.Context, id, actorUserID string) error {
	return r.updateTierLifecycleColumn(ctx, id, actorUserID, "tier.archived",
		`UPDATE service_tiers SET archived_at = NOW(), updated_at = NOW() WHERE id = $1`)
}

func (r *AccountRepository) UnarchiveServiceTier(ctx context.Context, id, actorUserID string) error {
	return r.updateTierLifecycleColumn(ctx, id, actorUserID, "tier.unarchived",
		`UPDATE service_tiers SET archived_at = NULL, updated_at = NOW() WHERE id = $1`)
}

// DeleteServiceTier menghapus tier permanen (S4P-11) -- HANYA tier custom
// (is_custom=true) yang sudah tidak dipakai grup manapun. Tier standar
// (starter/business/enterprise) tidak pernah bisa dihapus, cuma
// dinonaktifkan/archived.
func (r *AccountRepository) DeleteServiceTier(ctx context.Context, id, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.DeleteServiceTier: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var isCustom bool
	if err := tx.QueryRow(ctx, `SELECT is_custom FROM service_tiers WHERE id = $1`, id).Scan(&isCustom); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository.DeleteServiceTier: %w", domain.ErrTierNotFound)
		}
		return fmt.Errorf("repository.DeleteServiceTier: cek is_custom: %w", err)
	}
	if !isCustom {
		return fmt.Errorf("repository.DeleteServiceTier: %w", domain.ErrTierNotDeletable)
	}

	var inUseCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM groups WHERE tier_id = $1`, id).Scan(&inUseCount); err != nil {
		return fmt.Errorf("repository.DeleteServiceTier: cek pemakaian: %w", err)
	}
	if inUseCount > 0 {
		return fmt.Errorf("repository.DeleteServiceTier: %w", domain.ErrTierInUse)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM service_tiers WHERE id = $1`, id); err != nil {
		return fmt.Errorf("repository.DeleteServiceTier: delete: %w", err)
	}

	if err := logTierAudit(ctx, tx, actorUserID, "tier.deleted", id); err != nil {
		return fmt.Errorf("repository.DeleteServiceTier: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.DeleteServiceTier: commit tx: %w", err)
	}
	return nil
}

// FindUserIDByProviderSub resolve Keycloak subject (JWT "sub" claim) jadi
// users.id internal PRODO -- dipakai handler untuk mengisi invited_by/actor_id
// dari klaim JWT platform admin yang sedang login.
func (r *AccountRepository) FindUserIDByProviderSub(ctx context.Context, providerSub string) (userID string, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT user_id FROM user_auth_providers WHERE provider_sub = $1
	`, providerSub).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("repository.FindUserIDByProviderSub: %w", domain.ErrUserNotFound)
		}
		return "", fmt.Errorf("repository.FindUserIDByProviderSub: %w", err)
	}
	return userID, nil
}

// ActivationTarget adalah data yang dibutuhkan untuk memproses satu
// permintaan aktivasi akun (S1-06): siapa user-nya, keycloak sub-nya, dan
// ID baris platform_invitations untuk ditandai accepted setelah berhasil.
type ActivationTarget struct {
	InvitationID string
	UserID       string
	Email        string
	DisplayName  string
	KeycloakSub  string
}

// FindActivationTarget mencari invitation yang PENDING (belum accepted,
// belum expired) berdasarkan hash token mentah dari email. Tidak ditemukan
// -> domain.ErrInvitationNotFound (mencakup: token salah, sudah dipakai,
// atau sudah lewat 72 jam -- lihat docs/API_CONTRACT.md INVALID_OR_EXPIRED_TOKEN,
// satu kode error untuk ketiganya, sesuai kontrak yang sudah didokumentasikan).
func (r *AccountRepository) FindActivationTarget(ctx context.Context, tokenHash string) (*ActivationTarget, error) {
	t := &ActivationTarget{}
	err := r.db.QueryRow(ctx, `
		SELECT pi.id, u.id, u.email, u.display_name, uap.provider_sub
		FROM platform_invitations pi
		JOIN users u ON u.email = pi.email
		JOIN user_auth_providers uap ON uap.user_id = u.id
		WHERE pi.token_hash = $1
		  AND pi.accepted_at IS NULL
		  AND pi.expires_at > NOW()
	`, tokenHash).Scan(&t.InvitationID, &t.UserID, &t.Email, &t.DisplayName, &t.KeycloakSub)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindActivationTarget: %w", domain.ErrInvitationNotFound)
		}
		return nil, fmt.Errorf("repository.FindActivationTarget: %w", err)
	}
	return t, nil
}

// MarkInvitationAccepted menandai token aktivasi sudah dipakai -- one-time
// use, tidak bisa dipakai ulang meski belum kedaluwarsa (US-073 AC) -- dan
// mencatat audit trail langkah ini (US-073 AC: "seluruh aksi onboarding
// dicatat"), actor = user itu sendiri (self-service, belum aktif).
func (r *AccountRepository) MarkInvitationAccepted(ctx context.Context, invitationID, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.MarkInvitationAccepted: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	if _, err := tx.Exec(ctx, `
		UPDATE platform_invitations SET accepted_at = NOW() WHERE id = $1
	`, invitationID); err != nil {
		return fmt.Errorf("repository.MarkInvitationAccepted: update: %w", err)
	}

	if err := logAudit(ctx, tx, userID, "group_admin", "user.password_set", userID); err != nil {
		return fmt.Errorf("repository.MarkInvitationAccepted: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.MarkInvitationAccepted: commit tx: %w", err)
	}
	return nil
}

// MFAVerificationTarget adalah data yang dibutuhkan untuk memverifikasi OTP
// pertama (S1-07) -- token aktivasi sudah accepted di S1-06 (langkah
// password), jadi lookup di sini TIDAK mensyaratkan accepted_at IS NULL
// lagi, melainkan justru sebaliknya (IS NOT NULL, harus sudah lewat S1-06)
// dan MFA belum enabled (belum lewat S1-07).
type MFAVerificationTarget struct {
	UserID      string
	KeycloakSub string
}

// FindMFAVerificationTarget mencari target verifikasi OTP dari hash token
// yang sama dipakai di S1-06 -- token ini sudah "dipakai" (accepted) untuk
// langkah password, tapi tetap jadi referensi identitas yang sah untuk
// melanjutkan ke langkah MFA (satu alur aktivasi berkelanjutan).
func (r *AccountRepository) FindMFAVerificationTarget(ctx context.Context, tokenHash string) (*MFAVerificationTarget, error) {
	t := &MFAVerificationTarget{}
	err := r.db.QueryRow(ctx, `
		SELECT u.id, uap.provider_sub
		FROM platform_invitations pi
		JOIN users u ON u.email = pi.email
		JOIN user_auth_providers uap ON uap.user_id = u.id
		JOIN user_mfa_configs m ON m.user_id = u.id
		WHERE pi.token_hash = $1
		  AND pi.accepted_at IS NOT NULL
		  AND m.is_enabled = FALSE
	`, tokenHash).Scan(&t.UserID, &t.KeycloakSub)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindMFAVerificationTarget: %w", domain.ErrInvitationNotFound)
		}
		return nil, fmt.Errorf("repository.FindMFAVerificationTarget: %w", err)
	}
	return t, nil
}

// ActivateUser menandai akun aktif sepenuhnya setelah MFA terverifikasi
// (S1-07, langkah terakhir onboarding US-073) + audit trail.
func (r *AccountRepository) ActivateUser(ctx context.Context, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ActivateUser: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	if _, err := tx.Exec(ctx, `
		UPDATE users SET is_active = TRUE, updated_at = NOW() WHERE id = $1
	`, userID); err != nil {
		return fmt.Errorf("repository.ActivateUser: update: %w", err)
	}

	if err := logAudit(ctx, tx, userID, "group_admin", "user.activated", userID); err != nil {
		return fmt.Errorf("repository.ActivateUser: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ActivateUser: commit tx: %w", err)
	}
	return nil
}

// UserContact adalah info minimal untuk mengirim ulang email aktivasi (S1-08).
type UserContact struct {
	Email       string
	DisplayName string
}

// LoginUserRecord adalah data users yang dibutuhkan untuk login (S1-14) --
// cukup untuk mengecek status aktif dan menyusun field "user" pada respons
// POST /auth/login (API_CONTRACT.md §2), tanpa perlu decode JWT/provider_sub.
type LoginUserRecord struct {
	ID             string
	Email          string
	DisplayName    string
	PlatformRole   string
	IsActive       bool
	SuspendedAt    *time.Time
	AvatarURL      *string
	KeycloakUserID string
}

// FindUserForLogin mencari user berdasarkan email untuk keperluan login
// (S1-14). Tidak ditemukan -> domain.ErrUserNotFound (di-map ke
// ErrInvalidCredentials oleh service, supaya tidak membocorkan keberadaan
// email -- lihat domain.ErrInvalidCredentials).
//
// JOIN user_auth_providers (S3-38) untuk KeycloakUserID -- dipakai
// AuthService.LoginLocal mensinkron attribute Keycloak (prodo_platform_role/
// prodo_org_ids) sebelum menukar credential ke token, lihat IG-14.
// ORDER BY created_at + LIMIT 1 murni jaga-jaga: user_auth_providers TIDAK
// unique per user_id (skema §5.2 mengizinkan >1 provider), tapi alur
// aplikasi saat ini selalu cuma insert satu baris per user.
func (r *AccountRepository) FindUserForLogin(ctx context.Context, email string) (*LoginUserRecord, error) {
	u := &LoginUserRecord{}
	err := r.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.platform_role, u.is_active, u.suspended_at, u.avatar_url, uap.provider_sub
		FROM users u
		JOIN user_auth_providers uap ON uap.user_id = u.id
		WHERE u.email = $1 AND u.deleted_at IS NULL
		ORDER BY uap.created_at
		LIMIT 1
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PlatformRole, &u.IsActive, &u.SuspendedAt, &u.AvatarURL, &u.KeycloakUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindUserForLogin: %w", domain.ErrUserNotFound)
		}
		return nil, fmt.Errorf("repository.FindUserForLogin: %w", err)
	}
	return u, nil
}

// CheckIPAllowlist mengembalikan true kalau ip diperbolehkan login untuk
// userID (S4P-17): TIDAK ADA baris allowlist sama sekali untuk user ini
// berarti fitur belum dikonfigurasi -> selalu diperbolehkan (opsional,
// bukan wajib). Kalau ADA baris, ip harus cocok dengan SALAH SATU CIDR
// yang terdaftar. Satu query pakai operator containment native Postgres
// (`<<=`) -- lebih murah dan lebih benar daripada fetch semua baris lalu
// parse CIDR manual di Go.
func (r *AccountRepository) CheckIPAllowlist(ctx context.Context, userID, ip string) (allowed bool, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT
			NOT EXISTS(SELECT 1 FROM platform_admin_ip_allowlist WHERE user_id = $1)
			OR EXISTS(SELECT 1 FROM platform_admin_ip_allowlist WHERE user_id = $1 AND $2::inet <<= cidr)
	`, userID, ip).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("repository.CheckIPAllowlist: %w", err)
	}
	return allowed, nil
}

// ListOrgIDsForGroupAdmin mengembalikan seluruh organizations.id dalam grup
// yang dikelola userID (S3-38, implementation_gaps.md IG-01) -- dasar klaim
// JWT prodo_org_ids. Slice kosong (bukan error) untuk GA yang belum
// di-assign ke grup manapun.
// Dipanggil AuthService.syncKeycloakClaims SETIAP login, SEBELUM ada
// transaksi request-scoped/session variable RLS apapun -- query lewat
// function SQL SECURITY DEFINER prodo_group_admin_org_ids (migrasi
// 20260827110000), BUKAN JOIN langsung ke organizations dari sini.
// organizations kena FORCE ROW LEVEL SECURITY sejak S3-42; JOIN langsung
// dari pool prodo_app_user polos (tanpa app.current_user_id) akan diam-diam
// difilter RLS jadi 0 baris -- bootstrap ayam-telur (butuh org GA untuk isi
// klaim yang JUSTRU dipakai RLS organizations). Lihat implementation_gaps.md
// IG-14 (temuan lanjutan, ditemukan lewat live-test S3-40/41).
func (r *AccountRepository) ListOrgIDsForGroupAdmin(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM prodo_group_admin_org_ids($1)`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListOrgIDsForGroupAdmin: %w", err)
	}
	defer rows.Close()

	orgIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repository.ListOrgIDsForGroupAdmin: scan: %w", err)
		}
		orgIDs = append(orgIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListOrgIDsForGroupAdmin: %w", err)
	}
	return orgIDs, nil
}

// RecordLogin mencatat login berhasil (S1-20, US-001): memperbarui
// users.last_login_at dan menulis audit_logs (action 'user.login').
// Dipanggil setelah password (dan MFA, kalau berlaku) lolos verifikasi --
// bukan untuk percobaan gagal (rate-limiting percobaan gagal bukan scope
// audit trail, lihat security-compliance.md).
func (r *AccountRepository) RecordLogin(ctx context.Context, userID, platformRole string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.RecordLogin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("repository.RecordLogin: update last_login_at: %w", err)
	}
	if err := logAudit(ctx, tx, userID, platformRole, "user.login", userID); err != nil {
		return fmt.Errorf("repository.RecordLogin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.RecordLogin: commit tx: %w", err)
	}
	return nil
}

// FindUserByID mencari user berdasarkan users.id -- dipakai LoginSSO (S1-15)
// setelah provider_sub ditemukan di user_auth_providers.
func (r *AccountRepository) FindUserByID(ctx context.Context, userID string) (*LoginUserRecord, error) {
	u := &LoginUserRecord{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, display_name, platform_role, is_active, avatar_url
		FROM users WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PlatformRole, &u.IsActive, &u.AvatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindUserByID: %w", domain.ErrUserNotFound)
		}
		return nil, fmt.Errorf("repository.FindUserByID: %w", err)
	}
	return u, nil
}

// CreateSSOUser membuat akun PRODO baru untuk user SSO yang login pertama
// kali (S1-15, "auto-create akun jika user SSO pertama kali") -- langsung
// aktif (is_active=TRUE, tidak lewat alur onboarding aktivasi US-073 yang
// khusus untuk Group Admin) karena identitas sudah divouch oleh IdP.
// platform_role default 'member'. sso_config_id sengaja NULL -- resolusi ke
// sso_configs per-organisasi belum diimplementasikan (federation IdP
// eksternal masih stub di S1-13, enabled:false), lihat docs/s1-kickoff.html
// S1-15. Kalau email sudah dipakai user lain (mis. akun local existing) ->
// domain.ErrEmailAlreadyExists, TIDAK auto-link -- keputusan desain
// account-linking di-defer.
func (r *AccountRepository) CreateSSOUser(ctx context.Context, email, displayName, providerSub string) (*LoginUserRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("repository.CreateSSOUser: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	u := &LoginUserRecord{Email: email, DisplayName: displayName, PlatformRole: "member", IsActive: true}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, platform_role, is_active)
		VALUES ($1, $2, 'member', TRUE)
		RETURNING id
	`, email, displayName).Scan(&u.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("repository.CreateSSOUser: %w", domain.ErrEmailAlreadyExists)
		}
		return nil, fmt.Errorf("repository.CreateSSOUser: insert users: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO user_auth_providers (user_id, provider, provider_sub)
		VALUES ($1, 'keycloak_oidc', $2)
	`, u.ID, providerSub); err != nil {
		return nil, fmt.Errorf("repository.CreateSSOUser: insert user_auth_providers: %w", err)
	}

	if err := logAudit(ctx, tx, u.ID, "member", "user.sso_provisioned", u.ID); err != nil {
		return nil, fmt.Errorf("repository.CreateSSOUser: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("repository.CreateSSOUser: commit tx: %w", err)
	}
	return u, nil
}

// FindUserContactByID mencari email+display_name dari users.id -- dipakai
// S1-08 (resend activation) untuk tahu ke mana email baru dikirim.
func (r *AccountRepository) FindUserContactByID(ctx context.Context, userID string) (*UserContact, error) {
	c := &UserContact{}
	err := r.db.QueryRow(ctx, `
		SELECT email, display_name FROM users WHERE id = $1
	`, userID).Scan(&c.Email, &c.DisplayName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindUserContactByID: %w", domain.ErrUserNotFound)
		}
		return nil, fmt.Errorf("repository.FindUserContactByID: %w", err)
	}
	return c, nil
}

// FindUserIDByEmail -- dipakai S2-23 (invitation shortcut): kalau email
// yang diundang sudah terdaftar, AW menambahkannya langsung ke workspace
// alih-alih membuat undangan token baru. pgx.ErrNoRows (tidak di-wrap)
// kalau belum terdaftar -- caller cek via errors.Is, pola sama dengan
// WorkspaceMemberRepository.GetRole.
func (r *AccountRepository) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	var id string
	if err := r.db.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		return "", fmt.Errorf("repository.FindUserIDByEmail: %w", err)
	}
	return id, nil
}

// RegenerateInvitationToken meng-invalidate token lama dan menggantinya
// dengan yang baru (S1-08) -- UPDATE in-place, bukan INSERT baru, karena
// idx_platform_invitations_pending (partial unique index WHERE accepted_at
// IS NULL) hanya mengizinkan SATU invitation pending per email. Kalau tidak
// ada baris pending untuk email ini (sudah diaktivasi, atau tidak pernah
// diundang) -> domain.ErrInvitationNotFound.
func (r *AccountRepository) RegenerateInvitationToken(ctx context.Context, targetUserID, email, newTokenHash string, newExpiresAt time.Time, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.RegenerateInvitationToken: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE platform_invitations
		SET token_hash = $2, expires_at = $3, created_at = NOW()
		WHERE email = $1 AND accepted_at IS NULL
	`, email, newTokenHash, newExpiresAt)
	if err != nil {
		return fmt.Errorf("repository.RegenerateInvitationToken: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.RegenerateInvitationToken: %w", domain.ErrInvitationNotFound)
	}

	// entity_id = target Group Admin yang di-resend invitation-nya (yang
	// "dimutasi", sesuai docs/DATABASE_SCHEMA.md §5.27) -- actor_id tetap
	// Platform Admin yang melakukan aksi.
	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.activation_resent", targetUserID); err != nil {
		return fmt.Errorf("repository.RegenerateInvitationToken: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.RegenerateInvitationToken: commit tx: %w", err)
	}
	return nil
}

// SuspendGroupAdmin menandai users.suspended_at (S4P-02, US-067) -- HANYA
// untuk target platform_role='group_admin'. is_active TIDAK disentuh --
// suspend adalah state terpisah dari status onboarding (domain.
// ErrAccountSuspended), supaya reaktivasi tidak memaksa GA mengulang alur
// invite+aktivasi dari nol. 0 baris terpengaruh (target bukan GA atau tidak
// ada) -> domain.ErrUserNotFound.
func (r *AccountRepository) SuspendGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.SuspendGroupAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE users SET suspended_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND platform_role = 'group_admin'
	`, targetUserID)
	if err != nil {
		return fmt.Errorf("repository.SuspendGroupAdmin: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.SuspendGroupAdmin: %w", domain.ErrUserNotFound)
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.suspended", targetUserID); err != nil {
		return fmt.Errorf("repository.SuspendGroupAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.SuspendGroupAdmin: commit tx: %w", err)
	}
	return nil
}

// ReactivateGroupAdmin mengosongkan users.suspended_at (S4P-02, US-067) --
// GA langsung bisa login lagi tanpa mengulang aktivasi (is_active tidak
// pernah disentuh oleh suspend, jadi tetap TRUE dari sebelumnya).
func (r *AccountRepository) ReactivateGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ReactivateGroupAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE users SET suspended_at = NULL, updated_at = NOW()
		WHERE id = $1 AND platform_role = 'group_admin'
	`, targetUserID)
	if err != nil {
		return fmt.Errorf("repository.ReactivateGroupAdmin: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.ReactivateGroupAdmin: %w", domain.ErrUserNotFound)
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.reactivated", targetUserID); err != nil {
		return fmt.Errorf("repository.ReactivateGroupAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ReactivateGroupAdmin: commit tx: %w", err)
	}
	return nil
}

// CreatePlatformAdminInvitationParams -- S4P-37, US-084. JAUH lebih
// sederhana dari CreateGroupAdminInvitationParams -- Platform Admin tidak
// punya konsep grup/tier/kuota, jadi cuma email/display_name + infra
// undangan (Keycloak disabled user, token aktivasi, platform_invitations)
// yang dipakai ulang persis dari alur GA.
type CreatePlatformAdminInvitationParams struct {
	Email           string
	DisplayName     string
	KeycloakUserID  string
	TokenHash       string
	ExpiresAt       time.Time
	InvitedByUserID string
}

// CreatePlatformAdminInvitation menyimpan user baru
// (is_active=false, platform_role='platform_admin'), referensi
// Keycloak-nya, token aktivasi, dan entry platform_invitations. Aktivasi
// (set password + setup MFA) memakai ActivationService yang SAMA persis
// dengan Group Admin -- tidak ada percabangan role di sana.
func (r *AccountRepository) CreatePlatformAdminInvitation(ctx context.Context, p *CreatePlatformAdminInvitationParams) (userID string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, platform_role, is_active)
		VALUES ($1, $2, 'platform_admin', FALSE)
		RETURNING id
	`, p.Email, p.DisplayName).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: %w", domain.ErrEmailAlreadyExists)
		}
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: insert users: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO user_auth_providers (user_id, provider, provider_sub)
		VALUES ($1, 'local', $2)
	`, userID, p.KeycloakUserID); err != nil {
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: insert user_auth_providers: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO platform_invitations (email, platform_role, invited_by, token_hash, expires_at)
		VALUES ($1, 'platform_admin', $2, $3, $4)
	`, p.Email, p.InvitedByUserID, p.TokenHash, p.ExpiresAt); err != nil {
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: insert platform_invitations: %w", err)
	}

	if err := logAudit(ctx, tx, p.InvitedByUserID, "platform_admin", "user.invited", userID); err != nil {
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.CreatePlatformAdminInvitation: commit tx: %w", err)
	}
	return userID, nil
}

// PlatformAdminSummary -- satu baris GET /platform/admins (S4P-40).
type PlatformAdminSummary struct {
	ID          string
	Email       string
	DisplayName string
	IsActive    bool
	SuspendedAt *time.Time
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// ListPlatformAdmins -- S4P-40 (endpoint tambahan, tidak disebut literal
// di task S4P-37-39 -- dikonfirmasi user, dibutuhkan FE untuk mengisi
// tabel). Tidak perlu withPlatformAdminRLS -- users TIDAK di-RLS.
func (r *AccountRepository) ListPlatformAdmins(ctx context.Context) ([]PlatformAdminSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, email, display_name, is_active, suspended_at, last_login_at, created_at
		FROM users
		WHERE platform_role = 'platform_admin' AND deleted_at IS NULL
		ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("repository.ListPlatformAdmins: %w", err)
	}
	defer rows.Close()

	admins := make([]PlatformAdminSummary, 0)
	for rows.Next() {
		var a PlatformAdminSummary
		if err := rows.Scan(&a.ID, &a.Email, &a.DisplayName, &a.IsActive, &a.SuspendedAt, &a.LastLoginAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListPlatformAdmins: scan: %w", err)
		}
		admins = append(admins, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListPlatformAdmins: %w", err)
	}
	return admins, nil
}

// DeactivatePlatformAdmin -- S4P-38. Dua guard DALAM transaksi yang sama
// (hindari race antara cek dan update): target bukan diri sendiri (dicek
// di service, sebelum sampai sini, tapi query WHERE id != $1 di guard
// kedua tetap benar independen dari itu), dan minimal satu PA aktif
// tersisa SETELAH deactivate ini.
func (r *AccountRepository) DeactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.DeactivatePlatformAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var otherActiveCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM users
		WHERE platform_role = 'platform_admin' AND suspended_at IS NULL AND deleted_at IS NULL AND id != $1
	`, targetUserID).Scan(&otherActiveCount); err != nil {
		return fmt.Errorf("repository.DeactivatePlatformAdmin: cek PA aktif lain: %w", err)
	}
	if otherActiveCount == 0 {
		return domain.ErrMinimumActiveAdminRequired
	}

	tag, err := tx.Exec(ctx, `
		UPDATE users SET suspended_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND platform_role = 'platform_admin'
	`, targetUserID)
	if err != nil {
		return fmt.Errorf("repository.DeactivatePlatformAdmin: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPlatformAdminNotFound
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.suspended", targetUserID); err != nil {
		return fmt.Errorf("repository.DeactivatePlatformAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.DeactivatePlatformAdmin: commit tx: %w", err)
	}
	return nil
}

// ReactivatePlatformAdmin -- S4P-38 (tambahan, dikonfirmasi user --
// mirror pola suspend/reactivate Group Admin S4P-02, supaya PA yang
// dinonaktifkan tidak butuh pemulihan manual lewat DB).
func (r *AccountRepository) ReactivatePlatformAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ReactivatePlatformAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		UPDATE users SET suspended_at = NULL, updated_at = NOW()
		WHERE id = $1 AND platform_role = 'platform_admin'
	`, targetUserID)
	if err != nil {
		return fmt.Errorf("repository.ReactivatePlatformAdmin: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPlatformAdminNotFound
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.reactivated", targetUserID); err != nil {
		return fmt.Errorf("repository.ReactivatePlatformAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ReactivatePlatformAdmin: commit tx: %w", err)
	}
	return nil
}

// ResetPlatformAdminMFA -- S4P-39. Menghapus user_mfa_configs milik
// target -- login berikutnya otomatis diarahkan ke alur setup MFA ulang
// (mfa_setup_required, S4P-14/19), TANPA endpoint/logic tambahan.
func (r *AccountRepository) ResetPlatformAdminMFA(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.ResetPlatformAdminMFA: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND platform_role = 'platform_admin')
	`, targetUserID).Scan(&exists); err != nil {
		return fmt.Errorf("repository.ResetPlatformAdminMFA: cek target: %w", err)
	}
	if !exists {
		return domain.ErrPlatformAdminNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa_configs WHERE user_id = $1`, targetUserID); err != nil {
		return fmt.Errorf("repository.ResetPlatformAdminMFA: delete mfa: %w", err)
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.mfa_reset", targetUserID); err != nil {
		return fmt.Errorf("repository.ResetPlatformAdminMFA: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.ResetPlatformAdminMFA: commit tx: %w", err)
	}
	return nil
}

// TransferGroup memindahkan pengelolaan SEMUA grup dari fromUserID ke
// toUserID (S4P-03/04, IG-21). `organizations.group_id` TIDAK disentuh --
// organisasi tetap berada di grup yang sama, yang berubah cuma siapa GA
// pengelolanya (koreksi wording AC lama sprint_backlog.md yang menyebut
// "org berpindah group_id", audit S4 H4). toUserID harus akun
// platform_role='group_admin' yang valid, kalau tidak ->
// domain.ErrInvalidTransferTarget.
func (r *AccountRepository) TransferGroup(ctx context.Context, fromUserID, toUserID, actorUserID string) (transferredGroupCount int, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var targetIsGA bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND platform_role = 'group_admin' AND deleted_at IS NULL)
	`, toUserID).Scan(&targetIsGA); err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: cek target: %w", err)
	}
	if !targetIsGA {
		return 0, fmt.Errorf("repository.TransferGroup: %w", domain.ErrInvalidTransferTarget)
	}

	rows, err := tx.Query(ctx, `
		UPDATE group_admin_assignments
		SET user_id = $2, assigned_by = $3, assigned_at = NOW()
		WHERE user_id = $1
		RETURNING group_id
	`, fromUserID, toUserID, actorUserID)
	if err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: update: %w", err)
	}
	var groupIDs []string
	for rows.Next() {
		var gid string
		if err := rows.Scan(&gid); err != nil {
			rows.Close()
			return 0, fmt.Errorf("repository.TransferGroup: scan: %w", err)
		}
		groupIDs = append(groupIDs, gid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: rows: %w", err)
	}

	metadata, err := json.Marshal(map[string]any{
		"from_user_id": fromUserID,
		"to_user_id":   toUserID,
		"group_ids":    groupIDs,
	})
	if err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: encode metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id, metadata)
		VALUES ($1, 'platform_admin', 'group.transferred', 'user', $2, $3::jsonb)
	`, actorUserID, fromUserID, string(metadata)); err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("repository.TransferGroup: commit tx: %w", err)
	}
	return len(groupIDs), nil
}

// DeleteGroupAdmin menghapus (soft-delete via users.deleted_at, konsisten
// dengan FindUserForLogin/FindUserByID yang sudah memfilter deleted_at)
// akun Group Admin -- HANYA kalau dia sudah tidak mengelola grup manapun
// (S4P-05, IG-21). Transfer (S4P-03/04) memindahkan SELURUH assignment
// GA sekaligus (bukan per-grup), jadi cukup cek keberadaan baris
// group_admin_assignments -- GA yang sudah ditransfer otomatis 0 baris.
func (r *AccountRepository) DeleteGroupAdmin(ctx context.Context, targetUserID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.DeleteGroupAdmin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	var stillManagesGroup bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_admin_assignments WHERE user_id = $1)
	`, targetUserID).Scan(&stillManagesGroup); err != nil {
		return fmt.Errorf("repository.DeleteGroupAdmin: cek grup: %w", err)
	}
	if stillManagesGroup {
		return fmt.Errorf("repository.DeleteGroupAdmin: %w", domain.ErrGroupTransferRequired)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE users SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND platform_role = 'group_admin' AND deleted_at IS NULL
	`, targetUserID)
	if err != nil {
		return fmt.Errorf("repository.DeleteGroupAdmin: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.DeleteGroupAdmin: %w", domain.ErrUserNotFound)
	}

	if err := logAudit(ctx, tx, actorUserID, "platform_admin", "user.deleted", targetUserID); err != nil {
		return fmt.Errorf("repository.DeleteGroupAdmin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.DeleteGroupAdmin: commit tx: %w", err)
	}
	return nil
}

// GetPASessionIdleTimeoutSeconds membaca setting global session timeout
// Platform Admin (S4P-18, satu baris singleton platform_settings id=1)
// dalam detik -- dipakai FE PlatformSecuritySettings menampilkan nilai saat
// ini di form.
func (r *AccountRepository) GetPASessionIdleTimeoutSeconds(ctx context.Context) (int, error) {
	var seconds int
	if err := r.db.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM pa_session_idle_timeout)::int FROM platform_settings WHERE id = 1
	`).Scan(&seconds); err != nil {
		return 0, fmt.Errorf("repository.GetPASessionIdleTimeoutSeconds: %w", err)
	}
	return seconds, nil
}

// SetPASessionIdleTimeoutSeconds mengubah setting global session timeout
// Platform Admin (S4P-18) -- berlaku untuk SEMUA akun Platform Admin (bukan
// per-akun, beda dari IP allowlist), langsung tanpa redeploy karena dibaca
// dinamis oleh SessionRepository.TouchSessionFixed lewat subquery.
func (r *AccountRepository) SetPASessionIdleTimeoutSeconds(ctx context.Context, seconds int, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.SetPASessionIdleTimeoutSeconds: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	if _, err := tx.Exec(ctx, `
		UPDATE platform_settings SET pa_session_idle_timeout = make_interval(secs => $1), updated_at = NOW() WHERE id = 1
	`, seconds); err != nil {
		return fmt.Errorf("repository.SetPASessionIdleTimeoutSeconds: update: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id)
		VALUES ($1, 'platform_admin', 'platform_settings.session_timeout_changed', 'platform_settings', NULL)
	`, actorUserID); err != nil {
		return fmt.Errorf("repository.SetPASessionIdleTimeoutSeconds: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.SetPASessionIdleTimeoutSeconds: commit tx: %w", err)
	}
	return nil
}

// IPAllowlistEntry -- satu baris platform_admin_ip_allowlist untuk respons
// GET /platform/security-settings (S4P-18).
type IPAllowlistEntry struct {
	ID        string
	CIDR      string
	CreatedAt time.Time
}

// ListIPAllowlist mengembalikan entry allowlist milik SATU akun Platform
// Admin (S4P-18) -- self-service per akun, bukan lintas akun (beda dari
// session timeout yang global).
func (r *AccountRepository) ListIPAllowlist(ctx context.Context, userID string) ([]IPAllowlistEntry, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, cidr::text, created_at
		FROM platform_admin_ip_allowlist
		WHERE user_id = $1
		ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("repository.ListIPAllowlist: %w", err)
	}
	defer rows.Close()

	var entries []IPAllowlistEntry
	for rows.Next() {
		var e IPAllowlistEntry
		if err := rows.Scan(&e.ID, &e.CIDR, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("repository.ListIPAllowlist: scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.ListIPAllowlist: rows: %w", err)
	}
	return entries, nil
}

// AddIPAllowlistEntry menambah satu entry CIDR untuk userID (S4P-18).
// domain.ErrInvalidCIDR kalau cidr bukan notasi valid -- pengaman kedua,
// service layer sudah validasi lewat net.ParseCIDR sebelum sampai sini.
func (r *AccountRepository) AddIPAllowlistEntry(ctx context.Context, userID, cidr, actorUserID string) (id string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("repository.AddIPAllowlistEntry: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	err = tx.QueryRow(ctx, `
		INSERT INTO platform_admin_ip_allowlist (user_id, cidr) VALUES ($1, $2::cidr)
		RETURNING id
	`, userID, cidr).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return "", fmt.Errorf("repository.AddIPAllowlistEntry: %w", domain.ErrInvalidCIDR)
		}
		return "", fmt.Errorf("repository.AddIPAllowlistEntry: insert: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id)
		VALUES ($1, 'platform_admin', 'ip_allowlist.added', 'ip_allowlist', $2)
	`, actorUserID, id); err != nil {
		return "", fmt.Errorf("repository.AddIPAllowlistEntry: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.AddIPAllowlistEntry: commit tx: %w", err)
	}
	return id, nil
}

// DeleteIPAllowlistEntry menghapus satu entry -- HANYA kalau benar milik
// userID (S4P-18), sama pola ownership-check-di-WHERE seperti
// SessionRepository.RevokeSession, supaya satu PA tidak bisa menebak/hapus
// entry PA lain lewat ID.
func (r *AccountRepository) DeleteIPAllowlistEntry(ctx context.Context, userID, entryID, actorUserID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository.DeleteIPAllowlistEntry: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah Commit berhasil

	tag, err := tx.Exec(ctx, `
		DELETE FROM platform_admin_ip_allowlist WHERE id = $1 AND user_id = $2
	`, entryID, userID)
	if err != nil {
		return fmt.Errorf("repository.DeleteIPAllowlistEntry: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository.DeleteIPAllowlistEntry: %w", domain.ErrIPAllowlistEntryNotFound)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id)
		VALUES ($1, 'platform_admin', 'ip_allowlist.removed', 'ip_allowlist', $2)
	`, actorUserID, entryID); err != nil {
		return fmt.Errorf("repository.DeleteIPAllowlistEntry: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.DeleteIPAllowlistEntry: commit tx: %w", err)
	}
	return nil
}

// execer adalah subset pgxpool.Pool/pgx.Tx yang cukup untuk logAudit --
// supaya audit log bisa ditulis baik standalone maupun dalam transaksi
// pemanggil (atomicity dengan aksi utamanya).
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// logAudit menulis satu baris audit trail (US-073 AC: seluruh aksi onboarding
// dicatat). metadata kosong untuk entry sesederhana ini -- entity_id dipakai
// sebagai referensi utama. entity_type selalu 'user' -- seluruh pemanggil
// saat ini mencatat aksi atas entitas user (lint unparam).
//
// S4P-20/21, US-071: aksi ber-actor_role 'platform_admin' masuk ke
// platform_audit_logs (tabel terpisah, RLS SELECT-only untuk PA) alih-alih
// audit_logs biasa -- pemanggil TIDAK perlu berubah, cuma tabel tujuan yang
// beda tergantung actorRole. Lihat juga logTierAudit (selalu platform_admin).
func logAudit(ctx context.Context, exec execer, actorID, actorRole, action, entityID string) error {
	query := `INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id) VALUES ($1, $2, $3, 'user', $4)`
	if actorRole == "platform_admin" {
		query = `INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id) VALUES ($1, $2, $3, 'user', $4)`
	}
	if _, err := exec.Exec(ctx, query, actorID, actorRole, action, entityID); err != nil {
		return fmt.Errorf("logAudit: %w", err)
	}
	return nil
}

// logTierAudit -- sama seperti logAudit tapi entity_type='tier' (S4P-11),
// ke platform_audit_logs (S4P-20/21) karena actor_role selalu
// 'platform_admin' -- katalog tier cuma bisa diubah Platform Admin.
func logTierAudit(ctx context.Context, exec execer, actorID, action, entityID string) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO platform_audit_logs (actor_id, actor_role, action, entity_type, entity_id)
		VALUES ($1, 'platform_admin', $2, 'tier', $3)
	`, actorID, action, entityID); err != nil {
		return fmt.Errorf("logTierAudit: %w", err)
	}
	return nil
}
