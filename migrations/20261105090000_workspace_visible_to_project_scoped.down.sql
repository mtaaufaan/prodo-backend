ALTER POLICY workspaces_select ON workspaces
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_org(org_id)
    OR prodo_is_workspace_member(id)
  );

DROP FUNCTION prodo_is_project_member_of_workspace(uuid);
