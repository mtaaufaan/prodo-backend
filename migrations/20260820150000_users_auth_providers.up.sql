-- S1-01: Identity inti -- users + user_auth_providers.
-- Model Keycloak-delegated: password/credential dikelola Keycloak sepenuhnya,
-- PRODO cuma simpan referensi (provider + provider_sub) ke subject Keycloak.
-- Lihat docs/DATABASE_SCHEMA.md §5.1-5.2.
CREATE TYPE user_platform_role AS ENUM ('platform_admin', 'group_admin', 'member');

CREATE TABLE users (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email            VARCHAR(320) NOT NULL,
  display_name     VARCHAR(255) NOT NULL,
  avatar_url       TEXT,
  platform_role    user_platform_role NOT NULL DEFAULT 'member',
  is_active        BOOLEAN NOT NULL DEFAULT FALSE,
  locale           CHAR(2) NOT NULL DEFAULT 'id',
  last_login_at    TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at       TIMESTAMPTZ,
  CONSTRAINT uq_users_email UNIQUE (email)
);

-- sso_config_id (FK -> sso_configs) sengaja BELUM ditambahkan -- tabel
-- sso_configs belum ada di scope S1 (bagian dari epic Organization/SSO config
-- mendatang, lihat docs/design_gaps.md DG-03). Kolom + FK ditambahkan via
-- migration ALTER TABLE terpisah begitu sso_configs benar-benar dibuat.
CREATE TABLE user_auth_providers (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         VARCHAR(50) NOT NULL,
  provider_sub     VARCHAR(512) NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_auth_provider_sub UNIQUE (provider, provider_sub)
);

CREATE INDEX idx_user_auth_providers_user_id ON user_auth_providers (user_id);
