-- Rule workspace "DIWARISI" di halaman Rule Automation project (IG-119):
-- member project tanpa baris workspace_members (project-scoped-only, IG-107)
-- tidak bisa SELECT rule scope workspace pemilik project-nya karena policy
-- hanya memeriksa prodo_is_workspace_member. Tambahkan
-- prodo_is_project_member_of_workspace (sudah mencakup project_members dan
-- project_managers) -- hanya SELECT; INSERT/UPDATE tidak berubah.
DROP POLICY automation_rules_select ON automation_rules;
CREATE POLICY automation_rules_select ON automation_rules
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (
      prodo_is_group_admin_of_workspace(scope_id)
      OR prodo_is_workspace_member(scope_id)
      OR prodo_is_project_member_of_workspace(scope_id)
    ))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );
