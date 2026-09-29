-- Menutup TODO yang sudah tertulis sejak migrasi 20260827090000 sendiri
-- ("Cabang project-scoped cross-org (project_members) menyusul S3-19")
-- tapi tidak pernah benar-benar dibuat -- ditemukan user lewat gap-check
-- "project-scoped member tidak bisa masuk ke workspace tempat project-nya
-- berada sama sekali" (Home.tsx dead-end, WorkspaceLayout gagal resolve
-- role). Project-scoped member (project_members ATAU project_managers,
-- TANPA baris workspace_members) sebelum ini TIDAK PERNAH lolos RLS
-- workspaces_select -- workspace tempat project mereka berada bahkan
-- tidak terlihat lewat query paling dasar.
CREATE FUNCTION prodo_is_project_member_of_workspace(p_workspace_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE SECURITY DEFINER
AS $$
  SELECT EXISTS (
    SELECT 1 FROM project_members pm
    JOIN projects p ON p.id = pm.project_id
    WHERE p.workspace_id = p_workspace_id AND pm.user_id = prodo_current_user_id()
  ) OR EXISTS (
    SELECT 1 FROM project_managers pmg
    JOIN projects p ON p.id = pmg.project_id
    WHERE p.workspace_id = p_workspace_id AND pmg.user_id = prodo_current_user_id()
  )
$$;

ALTER POLICY workspaces_select ON workspaces
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_org(org_id)
    OR prodo_is_workspace_member(id)
    OR prodo_is_project_member_of_workspace(id)
  );
