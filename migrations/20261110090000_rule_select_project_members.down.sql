DROP POLICY automation_rules_select ON automation_rules;
CREATE POLICY automation_rules_select ON automation_rules
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );
