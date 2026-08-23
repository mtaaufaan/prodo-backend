DROP POLICY IF EXISTS wm_delete ON workspace_members;
CREATE POLICY wm_delete ON workspace_members
  FOR DELETE TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

DROP POLICY IF EXISTS wm_update ON workspace_members;
CREATE POLICY wm_update ON workspace_members
  FOR UPDATE TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

DROP POLICY IF EXISTS wm_insert ON workspace_members;
CREATE POLICY wm_insert ON workspace_members
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

DROP POLICY IF EXISTS wm_select ON workspace_members;
CREATE POLICY wm_select ON workspace_members
  FOR SELECT TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

DROP FUNCTION IF EXISTS prodo_is_group_admin_of_workspace(UUID);
