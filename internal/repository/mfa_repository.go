package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MFARepository menyimpan konfigurasi TOTP MFA per user
// (docs/DATABASE_SCHEMA.md §5.4). totp_secret dienkripsi via pgcrypto
// (pgp_sym_encrypt) memakai passphrase dari MFA_ENCRYPTION_KEY -- di-encode
// base64 di level SQL supaya muat di kolom TEXT.
type MFARepository struct {
	db            *pgxpool.Pool
	encryptionKey string
}

// NewMFARepository mengembalikan error kalau encryptionKey kosong --
// pgp_sym_encrypt/decrypt DIAM-DIAM berhasil dengan passphrase kosong, jadi
// tanpa pengecekan ini, MFA_ENCRYPTION_KEY yang lupa diisi tidak akan
// ketahuan sampai insiden nyata (totp_secret "terenkripsi" pakai kunci
// kosong). Fail fast di startup, konsisten dengan keycloak.NewAdminClient.
func NewMFARepository(db *pgxpool.Pool, encryptionKey string) (*MFARepository, error) {
	if encryptionKey == "" {
		return nil, fmt.Errorf("repository.NewMFARepository: MFA_ENCRYPTION_KEY wajib diisi")
	}
	return &MFARepository{db: db, encryptionKey: encryptionKey}, nil
}

// SaveTOTPSecret menyimpan (upsert) secret TOTP baru untuk user, is_enabled
// tetap FALSE sampai OTP pertama diverifikasi (S1-07).
func (r *MFARepository) SaveTOTPSecret(ctx context.Context, userID, totpSecret string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_mfa_configs (user_id, is_enabled, totp_secret)
		VALUES ($1, FALSE, encode(pgp_sym_encrypt($2, $3), 'base64'))
		ON CONFLICT (user_id) DO UPDATE
		SET totp_secret = EXCLUDED.totp_secret, is_enabled = FALSE, updated_at = NOW()
	`, userID, totpSecret, r.encryptionKey)
	if err != nil {
		return fmt.Errorf("repository.SaveTOTPSecret: %w", err)
	}
	return nil
}

// GetTOTPSecret mendekripsi dan mengembalikan totp_secret user (S1-07).
func (r *MFARepository) GetTOTPSecret(ctx context.Context, userID string) (string, error) {
	var secret string
	err := r.db.QueryRow(ctx, `
		SELECT pgp_sym_decrypt(decode(totp_secret, 'base64'), $2)
		FROM user_mfa_configs WHERE user_id = $1
	`, userID, r.encryptionKey).Scan(&secret)
	if err != nil {
		return "", fmt.Errorf("repository.GetTOTPSecret: %w", err)
	}
	return secret, nil
}

// EnableMFA menandai MFA aktif setelah OTP pertama berhasil diverifikasi.
func (r *MFARepository) EnableMFA(ctx context.Context, userID string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE user_mfa_configs SET is_enabled = TRUE, updated_at = NOW() WHERE user_id = $1
	`, userID); err != nil {
		return fmt.Errorf("repository.EnableMFA: %w", err)
	}
	return nil
}

// SaveBackupCodes menyimpan 10 hash kode cadangan (S1-07/S1-10) --
// menggantikan seluruh isi kolom, dipanggil sekali tepat setelah MFA
// diaktifkan pertama kali.
func (r *MFARepository) SaveBackupCodes(ctx context.Context, userID string, hashedCodes []string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE user_mfa_configs SET backup_codes = $2, updated_at = NOW() WHERE user_id = $1
	`, userID, hashedCodes); err != nil {
		return fmt.Errorf("repository.SaveBackupCodes: %w", err)
	}
	return nil
}

// GetMFAStatus mengembalikan status MFA user untuk verifikasi saat login
// (S1-17). Tidak ada baris sama sekali (belum pernah setup MFA) BUKAN
// error -- isEnabled=false, secret="" dikembalikan, caller (MFAService)
// yang memutuskan kebijakan wajib/opsional per role.
func (r *MFARepository) GetMFAStatus(ctx context.Context, userID string) (isEnabled bool, secret string, err error) {
	var encSecret *string
	err = r.db.QueryRow(ctx, `
		SELECT is_enabled, pgp_sym_decrypt(decode(totp_secret, 'base64'), $2)
		FROM user_mfa_configs WHERE user_id = $1
	`, userID, r.encryptionKey).Scan(&isEnabled, &encSecret)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("repository.GetMFAStatus: %w", err)
	}
	if encSecret != nil {
		secret = *encSecret
	}
	return isEnabled, secret, nil
}
