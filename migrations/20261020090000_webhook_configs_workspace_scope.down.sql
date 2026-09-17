DROP POLICY webhook_deliveries_insert ON webhook_deliveries;
CREATE POLICY webhook_deliveries_insert ON webhook_deliveries
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id AND prodo_is_group_admin_of_group(wc.group_id)
  ));

DROP POLICY webhook_deliveries_select ON webhook_deliveries;
CREATE POLICY webhook_deliveries_select ON webhook_deliveries
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM webhook_configs wc WHERE wc.id = webhook_deliveries.webhook_id AND prodo_is_group_admin_of_group(wc.group_id)
  ));

DROP POLICY webhook_configs_delete ON webhook_configs;
CREATE POLICY webhook_configs_delete ON webhook_configs
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

DROP POLICY webhook_configs_update ON webhook_configs;
CREATE POLICY webhook_configs_update ON webhook_configs
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

DROP POLICY webhook_configs_insert ON webhook_configs;
CREATE POLICY webhook_configs_insert ON webhook_configs
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

DROP POLICY webhook_configs_select ON webhook_configs;
CREATE POLICY webhook_configs_select ON webhook_configs
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

DROP INDEX idx_webhook_configs_workspace_id;
ALTER TABLE webhook_configs DROP CONSTRAINT chk_webhook_configs_project_scope;
ALTER TABLE webhook_configs DROP CONSTRAINT chk_webhook_configs_scope;
ALTER TABLE webhook_configs DROP COLUMN project_id;
ALTER TABLE webhook_configs DROP COLUMN workspace_id;
ALTER TABLE webhook_configs ALTER COLUMN group_id SET NOT NULL;
