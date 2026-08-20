package repository

import (
	"context"
	"fmt"

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

func NewMFARepository(db *pgxpool.Pool, encryptionKey string) *MFARepository {
	return &MFARepository{db: db, encryptionKey: encryptionKey}
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
