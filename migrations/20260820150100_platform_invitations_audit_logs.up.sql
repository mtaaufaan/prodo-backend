-- S1-02: token aktivasi Group Admin (level platform, TANPA workspace -- GA
-- berada di atas organisasi dalam hierarki PRODO) + audit trail generik.
--
-- platform_invitations sengaja tabel TERPISAH dari user_invitations
-- (docs/DATABASE_SCHEMA.md §5.30), bukan reuse dengan workspace_id nullable:
-- user_invitations.role pakai tipe workspace_role yang tidak cocok untuk
-- platform_role GA, dan mencampur dua konsep berbeda (workspace member invite
-- vs platform-level admin invite) dalam satu tabel akan bikin constraint
-- (mis. unique per workspace+email) jadi ambigu untuk kasus platform-level.
CREATE TABLE platform_invitations (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email          VARCHAR(320) NOT NULL,
  platform_role  user_platform_role NOT NULL DEFAULT 'group_admin',
  invited_by     UUID NOT NULL REFERENCES users(id),
  token_hash     TEXT NOT NULL, -- SHA-256 hash; token plaintext hanya ada di link email
  expires_at     TIMESTAMPTZ NOT NULL, -- created_at + 72 jam
  accepted_at    TIMESTAMPTZ, -- NULL = belum diaktivasi
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Satu undangan platform pending per email (partial unique index -- sama
-- pola dengan idx_invitations_pending di user_invitations, docs §5.30).
CREATE UNIQUE INDEX idx_platform_invitations_pending
  ON platform_invitations (email) WHERE accepted_at IS NULL;

-- audit_logs -- lihat docs/DATABASE_SCHEMA.md §5.27. org_id/workspace_id
-- sengaja TANPA FK ke organizations/workspaces -- kedua tabel itu belum ada
-- di scope S1 (epic Organization & Workspace Management mendatang). FK
-- ditambahkan via migration ALTER TABLE terpisah begitu tabel itu dibuat.
CREATE TABLE audit_logs (
  id           UUID DEFAULT uuid_generate_v4(),
  org_id       UUID,
  workspace_id UUID,
  actor_id     UUID,
  actor_role   TEXT, -- workspace_role atau platform role ('platform_admin', 'group_admin')
  actor_ip     INET,
  action       VARCHAR(100) NOT NULL,
  entity_type  VARCHAR(50) NOT NULL,
  entity_id    UUID,
  state_before JSONB,
  state_after  JSONB,
  metadata     JSONB,
  logged_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (logged_at);

CREATE TABLE audit_logs_2025 PARTITION OF audit_logs
  FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
CREATE TABLE audit_logs_2026 PARTITION OF audit_logs
  FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
CREATE TABLE audit_logs_2027 PARTITION OF audit_logs
  FOR VALUES FROM ('2027-01-01') TO ('2028-01-01');

CREATE INDEX idx_audit_logs_org_date ON audit_logs (org_id, logged_at DESC);
CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_id);
CREATE INDEX idx_audit_logs_entity ON audit_logs (entity_type, entity_id);
