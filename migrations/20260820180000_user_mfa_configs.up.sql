-- S1-06: TOTP MFA config per user -- lihat docs/DATABASE_SCHEMA.md §5.4.
-- totp_secret/backup_codes disimpan terenkripsi via pgcrypto (pgp_sym_encrypt),
-- di-encode base64 supaya muat di kolom TEXT sesuai tipe yang didokumentasikan.
CREATE TABLE user_mfa_configs (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  is_enabled   BOOLEAN NOT NULL DEFAULT FALSE,
  totp_secret  TEXT,
  backup_codes TEXT[],
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_user_mfa_configs_user_id UNIQUE (user_id)
);
