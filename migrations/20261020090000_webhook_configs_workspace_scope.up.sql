-- Webhook level workspace (S4W-14, US-054, desain "AW Webhook.dc.html" +
-- "AW Add Webhook.dc.html") -- webhook_configs (migrasi 20260920090000)
-- didesain KHUSUS untuk grup (Track S4G), group_id NOT NULL. Kickoff plan
-- S4W-14 eksplisit minta "reuse WebhookService" untuk level workspace baru
-- -- generalisasi tabel ini (bukan tabel terpisah) supaya satu titik audit
-- untuk risiko cross-scope yang dicatat kickoff plan sendiri: "AW workspace
-- A tidak lihat webhook workspace B; tidak bentrok dengan webhook GA-level".
--
-- group_id jadi NULLABLE, workspace_id ditambah -- PERSIS SATU dari
-- keduanya terisi (CHECK), sama pola org_id nullable = "seluruh grup" di
-- migrasi lama. project_id (opsional, HANYA valid kalau workspace_id
-- terisi) mengimplementasikan opsi cakupan "Project X" di LINGKUP desain
-- "AW Add Webhook.dc.html" -- NULL berarti "Seluruh workspace".
ALTER TABLE webhook_configs ALTER COLUMN group_id DROP NOT NULL;
ALTER TABLE webhook_configs ADD COLUMN workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE webhook_configs ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE CASCADE;

ALTER TABLE webhook_configs ADD CONSTRAINT chk_webhook_configs_scope
  CHECK ((group_id IS NOT NULL AND workspace_id IS NULL) OR (group_id IS NULL AND workspace_id IS NOT NULL));

ALTER TABLE webhook_configs ADD CONSTRAINT chk_webhook_configs_project_scope
  CHECK (project_id IS NULL OR workspace_id IS NOT NULL);

CREATE INDEX idx_webhook_configs_workspace_id ON webhook_configs (workspace_id);

-- RLS -- cabang workspace REUSE fungsi existing (prodo_is_group_admin_of_
-- workspace/prodo_is_workspace_member, dari custom_statuses/workspaces),
-- TIDAK bikin fungsi baru. Longgar di level RLS (member mana pun boleh
-- SELECT/INSERT/UPDATE/DELETE lolos RLS) -- sama pola custom_statuses/
-- workspace mention-settings: penegakan AW-only SEBENARNYA ada di
-- WebhookService (authorizeScope), bukan RLS. RLS di sini cuma mencegah
-- kebocoran ANTAR workspace/grup, bukan antar role dalam satu workspace.
DROP POLICY webhook_configs_select ON webhook_configs;
CREATE POLICY webhook_configs_select ON webhook_configs
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (group_id IS NOT NULL AND prodo_is_group_admin_of_group(group_id))
    OR (workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(workspace_id) OR prodo_is_workspace_member(workspace_id)))
  );

DROP POLICY webhook_configs_insert ON webhook_configs;
CREATE POLICY webhook_configs_insert ON webhook_configs
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR (group_id IS NOT NULL AND prodo_is_group_admin_of_group(group_id))
    OR (workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(workspace_id) OR prodo_is_workspace_member(workspace_id)))
  );

DROP POLICY webhook_configs_update ON webhook_configs;
CREATE POLICY webhook_configs_update ON webhook_configs
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (group_id IS NOT NULL AND prodo_is_group_admin_of_group(group_id))
    OR (workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(workspace_id) OR prodo_is_workspace_member(workspace_id)))
  );

DROP POLICY webhook_configs_delete ON webhook_configs;
CREATE POLICY webhook_configs_delete ON webhook_configs
  FOR DELETE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (group_id IS NOT NULL AND prodo_is_group_admin_of_group(group_id))
    OR (workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(workspace_id) OR prodo_is_workspace_member(workspace_id)))
  );

-- webhook_deliveries join balik ke webhook_configs -- perluas EXISTS
-- dengan cabang workspace yang sama.
DROP POLICY webhook_deliveries_select ON webhook_deliveries;
CREATE POLICY webhook_deliveries_select ON webhook_deliveries
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id
      AND (
        (wc.group_id IS NOT NULL AND prodo_is_group_admin_of_group(wc.group_id))
        OR (wc.workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(wc.workspace_id) OR prodo_is_workspace_member(wc.workspace_id)))
      )
  ));

DROP POLICY webhook_deliveries_insert ON webhook_deliveries;
CREATE POLICY webhook_deliveries_insert ON webhook_deliveries
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id
      AND (
        (wc.group_id IS NOT NULL AND prodo_is_group_admin_of_group(wc.group_id))
        OR (wc.workspace_id IS NOT NULL AND (prodo_is_group_admin_of_workspace(wc.workspace_id) OR prodo_is_workspace_member(wc.workspace_id)))
      )
  ));
