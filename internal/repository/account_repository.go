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

// AccountRepository menyimpan data akun -- tabel platform-level (users,
// user_auth_providers, platform_invitations, audit_logs), bukan tabel
// tenant-scoped, jadi TIDAK melewati RLS (lihat docs/RLS_DESIGN.md §8: tabel
// ini sengaja tidak di-RLS karena belum terikat org_id/workspace_id).
type AccountRepository struct {
	db *pgxpool.Pool
}

func NewAccountRepository(db *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{db: db}
}

// CreateGroupAdminInvitationParams membungkus seluruh data yang dibutuhkan
// untuk provisioning satu akun Group Admin. Semua insert (users,
// user_auth_providers, platform_invitations, audit_logs) terjadi dalam satu
// transaksi -- all-or-nothing, lihat docs/DATABASE_SCHEMA.md §5.1/5.2/5.27
// dan migrations/20260820150000_users_auth_providers.up.sql +
// 20260820150100_platform_invitations_audit_logs.up.sql.
type CreateGroupAdminInvitationParams struct {
	Email           string
	DisplayName     string
	KeycloakUserID  string
	TokenHash       string
	ExpiresAt       time.Time
	InvitedByUserID string
}

// CreateGroupAdminInvitation menyimpan user baru (is_active=false,
// platform_role='group_admin'), referensi Keycloak-nya, token aktivasi, dan
// entry audit trail (US-073 AC: "seluruh aksi onboarding dicatat"). Kalau
// email sudah ada, mengembalikan domain.ErrEmailAlreadyExists.
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

	metadata, err := json.Marshal(map[string]string{
		"email":         p.Email,
		"platform_role": "group_admin",
	})
	if err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: encode metadata: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id, metadata)
		VALUES ($1, 'platform_admin', 'user.invited', 'user', $2, $3::jsonb)
	`, p.InvitedByUserID, userID, string(metadata)); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: insert audit_logs: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("repository.CreateGroupAdminInvitation: commit tx: %w", err)
	}
	return userID, nil
}

// GroupAdminSummary adalah satu baris daftar Group Admin untuk panel
// Platform Admin (S1-12).
type GroupAdminSummary struct {
	ID          string
	Email       string
	DisplayName string
	IsActive    bool
	CreatedAt   time.Time
}

// ListGroupAdmins mengembalikan seluruh user dengan platform_role='group_admin',
// terbaru dulu, dengan pagination sederhana (docs/coding-conventions.md §7.1).
func (r *AccountRepository) ListGroupAdmins(ctx context.Context, limit, offset int) ([]GroupAdminSummary, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM users WHERE platform_role = 'group_admin'
	`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repository.ListGroupAdmins: count: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, email, display_name, is_active, created_at
		FROM users
		WHERE platform_role = 'group_admin'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repository.ListGroupAdmins: query: %w", err)
	}
	defer rows.Close()

	var result []GroupAdminSummary
	for rows.Next() {
		var s GroupAdminSummary
		if err := rows.Scan(&s.ID, &s.Email, &s.DisplayName, &s.IsActive, &s.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("repository.ListGroupAdmins: scan: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repository.ListGroupAdmins: rows: %w", err)
	}
	return result, total, nil
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
	ID           string
	Email        string
	DisplayName  string
	PlatformRole string
	IsActive     bool
	AvatarURL    *string
}

// FindUserForLogin mencari user berdasarkan email untuk keperluan login
// (S1-14). Tidak ditemukan -> domain.ErrUserNotFound (di-map ke
// ErrInvalidCredentials oleh service, supaya tidak membocorkan keberadaan
// email -- lihat domain.ErrInvalidCredentials).
func (r *AccountRepository) FindUserForLogin(ctx context.Context, email string) (*LoginUserRecord, error) {
	u := &LoginUserRecord{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, display_name, platform_role, is_active, avatar_url
		FROM users WHERE email = $1 AND deleted_at IS NULL
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PlatformRole, &u.IsActive, &u.AvatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("repository.FindUserForLogin: %w", domain.ErrUserNotFound)
		}
		return nil, fmt.Errorf("repository.FindUserForLogin: %w", err)
	}
	return u, nil
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

// execer adalah subset pgxpool.Pool/pgx.Tx yang cukup untuk logAudit --
// supaya audit log bisa ditulis baik standalone maupun dalam transaksi
// pemanggil (atomicity dengan aksi utamanya).
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// logAudit menulis satu baris audit_logs (US-073 AC: seluruh aksi onboarding
// dicatat). metadata kosong untuk entry sesederhana ini -- entity_id dipakai
// sebagai referensi utama. entity_type selalu 'user' -- seluruh pemanggil
// saat ini mencatat aksi atas entitas user (lint unparam).
func logAudit(ctx context.Context, exec execer, actorID, actorRole, action, entityID string) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id)
		VALUES ($1, $2, $3, 'user', $4)
	`, actorID, actorRole, action, entityID); err != nil {
		return fmt.Errorf("logAudit: %w", err)
	}
	return nil
}
