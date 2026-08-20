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
// use, tidak bisa dipakai ulang meski belum kedaluwarsa (US-073 AC).
func (r *AccountRepository) MarkInvitationAccepted(ctx context.Context, invitationID string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE platform_invitations SET accepted_at = NOW() WHERE id = $1
	`, invitationID); err != nil {
		return fmt.Errorf("repository.MarkInvitationAccepted: %w", err)
	}
	return nil
}
