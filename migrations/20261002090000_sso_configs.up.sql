-- Konfigurasi SSO per Organisasi (US-074, dipindahkan ke Track S4G S4G-23/24
-- 2026-08-30 -- lihat sprint_backlog.md). Skema PERSIS DATABASE_SCHEMA.md
-- §5.8 (`sso_configs`), KECUALI `sso_scim_tokens` -- SCIM offboarding TETAP
-- di luar cakupan (API_CONTRACT.md Appendix A "Deferred Endpoints"), bukan
-- bagian S4G-23/24. Cakupan S4G-23/24 SENGAJA lebih kecil dari draft asli
-- S12-27..33 (14 SP): test koneksi IdP (S12-29), enforcement auth_mode
-- (S12-30), reset password massal saat SSO dinonaktifkan (S12-31), dan
-- registrasi IdP dinamis ke Keycloak (S12-32) TIDAK ikut dipindah ke Track
-- S4G -- migrasi ini CUMA penyimpanan konfigurasi (S4G-23: 3 SP, S4G-24:
-- 2 SP = 5 SP total), lihat implementation_gaps.md untuk gap ini.
--
-- organizations.sso_enabled SUDAH ADA sejak migrasi S3 (§5.7) tapi belum
-- pernah ditulis kode manapun (lihat komentar InvitationService.AcceptInvitation) --
-- migrasi ini TIDAK menambah kolom baru di situ, toggle-nya diekspos lewat
-- PUT /organizations/:id/sso-config (satu form, bukan endpoint terpisah).

CREATE TYPE auth_protocol AS ENUM ('saml2', 'oidc');

CREATE TABLE sso_configs (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  protocol          auth_protocol NOT NULL,
  idp_entity_id     VARCHAR(512),
  idp_metadata_url  TEXT,
  idp_metadata_xml  TEXT,
  client_id         VARCHAR(512),
  client_secret     TEXT,                        -- [ENC] pgp_sym_encrypt, pola sama webhook_configs.hmac_secret_encrypted
  discovery_url     TEXT,
  is_tested         BOOLEAN NOT NULL DEFAULT FALSE,
  attribute_mapping JSONB NOT NULL DEFAULT '{"email":"email","full_name":"name","groups":null}',
  last_test_at      TIMESTAMPTZ,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_sso_org UNIQUE (org_id)
);

ALTER TABLE sso_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE sso_configs FORCE ROW LEVEL SECURITY;

-- Pola PERSIS audit_logs_select_group_admin (migrasi 20260922090000): join
-- org_id -> organizations.group_id, reuse prodo_is_group_admin_of_group
-- yang sudah ada -- TIDAK ada fungsi RLS baru.
CREATE POLICY sso_configs_all ON sso_configs
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM organizations o
      WHERE o.id = sso_configs.org_id AND prodo_is_group_admin_of_group(o.group_id)
    )
  );
