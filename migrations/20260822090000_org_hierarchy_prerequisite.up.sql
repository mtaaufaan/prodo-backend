-- Prasyarat teknis S2-01 (bukan S2 sendiri) -- workspace_members (S2-01) FK ke
-- workspaces.id, yang FK ke organizations.id, yang FK ke groups.id. Ketiga
-- tabel ini literally dijadwalkan S12-01/S3-01/S3-08 (Sprint 12/3), TAPI
-- workspace_members tidak bisa dibuat dengan FK yang benar tanpa ketiganya
-- ada lebih dulu -- sprint_backlog.md menjadwalkan S2 SEBELUM S3/S12 padahal
-- ada dependency FK terbalik. Didiskusikan dan dikonfirmasi user: majukan
-- migrasi MINIMAL ketiga tabel ini sekarang (kolom persis sesuai
-- DATABASE_SCHEMA.md §5.5/5.7/5.9, bukan wording S12-01 yang sudah usang --
-- lihat catatan "owner_account_id"/"service_tiers" di S12-01 yang tidak ada
-- di skema final), FK utuh dari awal. Kolom/fitur lain yang jadi scope penuh
-- S3/S12 (sso_configs, storage dashboard, group_admin_assignments, dst)
-- TETAP dikerjakan nanti sesuai jadwal aslinya -- migrasi ini cuma cukup
-- untuk membuat FK workspace_members valid.
CREATE TABLE groups (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name        VARCHAR(255) NOT NULL,
  tier        VARCHAR(20) NOT NULL DEFAULT 'starter',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT chk_groups_tier CHECK (tier IN ('starter', 'professional', 'enterprise'))
);

CREATE TABLE organizations (
  id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
  name                VARCHAR(255) NOT NULL,
  slug                VARCHAR(100) NOT NULL,
  sso_enabled         BOOLEAN NOT NULL DEFAULT FALSE,
  storage_quota_mb    BIGINT NOT NULL DEFAULT 10240,
  storage_used_mb     BIGINT NOT NULL DEFAULT 0,
  retention_days      INTEGER NOT NULL DEFAULT 90,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  contract_end_at     TIMESTAMPTZ,
  purge_scheduled_at  TIMESTAMPTZ,
  CONSTRAINT uq_organizations_slug UNIQUE (slug),
  CONSTRAINT chk_organizations_retention CHECK (retention_days BETWEEN 30 AND 365)
);

CREATE INDEX idx_organizations_group_id ON organizations (group_id);

CREATE TABLE workspaces (
  id                          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  org_id                      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name                        VARCHAR(255) NOT NULL,
  mention_cooldown_minutes    SMALLINT NOT NULL DEFAULT 10,
  created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  archived_at                 TIMESTAMPTZ,
  CONSTRAINT chk_workspaces_cooldown CHECK (mention_cooldown_minutes BETWEEN 10 AND 30)
);

CREATE INDEX idx_workspaces_org_id ON workspaces (org_id);
