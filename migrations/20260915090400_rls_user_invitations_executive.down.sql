DROP POLICY IF EXISTS invitations_select ON user_invitations;
CREATE POLICY invitations_select ON user_invitations
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_workspace_member(workspace_id)
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

DROP POLICY IF EXISTS invitations_insert ON user_invitations;
CREATE POLICY invitations_insert ON user_invitations
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

DROP POLICY IF EXISTS invitations_update ON user_invitations;
CREATE POLICY invitations_update ON user_invitations
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );
