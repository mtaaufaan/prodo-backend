-- S4G Data Retention: arsip ekspor sebelum penghapusan (desain "GA Data
-- Retention.dc.html" modal "Ekspor Data Sebelum Penghapusan"). Payload
-- CUMA metadata yang sungguhan ada di DB (nama org/workspace/project,
-- jumlah member/workspace/project) -- folder /data (task) dan /attachments
-- dari desain SENGAJA kosong, tabel tasks/task_attachments upload belum
-- ada (implementation_gaps.md IG-19), dikonfirmasi user. token_hash sama
-- pola user_invitations (SHA-256, token mentah cuma dikirim lewat email).
CREATE TABLE retention_exports (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  kind         VARCHAR(10) NOT NULL,
  item_name    VARCHAR(255) NOT NULL,
  payload      JSONB NOT NULL,
  token_hash   TEXT NOT NULL,
  requested_by UUID NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at   TIMESTAMPTZ NOT NULL
);

ALTER TABLE retention_exports ADD CONSTRAINT chk_retention_exports_kind CHECK (kind IN ('org', 'workspace', 'project'));

CREATE INDEX idx_retention_exports_group_id ON retention_exports (group_id);

ALTER TABLE retention_exports ENABLE ROW LEVEL SECURITY;
ALTER TABLE retention_exports FORCE ROW LEVEL SECURITY;

CREATE POLICY retention_exports_insert ON retention_exports
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

-- SELECT juga mengizinkan prodo_is_platform_admin() TANPA syarat lain --
-- rute unduhan publik (token-based, tanpa sesi JWT) jalan lewat konteks
-- bypass platform_admin (pola sama InvitationHandler.AcceptInvitation).
CREATE POLICY retention_exports_select ON retention_exports
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));
