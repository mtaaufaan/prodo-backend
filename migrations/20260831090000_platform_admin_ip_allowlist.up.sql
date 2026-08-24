-- S4P-17 (US-070, implementation_gaps.md IG-20): IP allowlist opsional per
-- akun Platform Admin. Kosong (tidak ada baris untuk user_id tertentu)
-- berarti TIDAK dibatasi -- fitur ini opsional, bukan wajib (beda dari MFA
-- yang wajib tanpa pengecualian). Bisa punya banyak baris (banyak CIDR)
-- per PA, bukan satu field tunggal, supaya PA bisa allowlist lebih dari
-- satu jaringan (kantor + VPN, misalnya).
--
-- TIDAK diberi RLS -- tabel level-platform lain (mis. groups) juga belum
-- diproteksi RLS, tercatat sebagai gap yang sama (implementation_gaps.md
-- IG-07). Tabel ini hanya pernah diakses dari AccountRepository memakai
-- pool langsung (pre-auth context, pola sama dengan FindUserForLogin),
-- bukan lewat exec RLS-scoped per-request.
CREATE TABLE platform_admin_ip_allowlist (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  cidr       CIDR NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, cidr)
);

CREATE INDEX idx_platform_admin_ip_allowlist_user_id ON platform_admin_ip_allowlist (user_id);
