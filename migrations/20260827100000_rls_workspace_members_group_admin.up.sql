-- S3-41 (implementation_gaps.md IG-01): workspace_members RLS (S2-10) tidak
-- pernah punya jalur Group Admin sama sekali -- cuma prodo_is_workspace_member
-- OR prodo_is_platform_admin(). Ketahuan lewat live-test S3-09 (WorkspaceService.
-- CreateWorkspace, yang reuse RBACService.AssignRole buat menunjuk Admin
-- Workspace): GA yang BARU membuat workspace belum jadi workspace_members
-- di situ, jadi INSERT assignment role AW-nya sendiri DITOLAK RLS -- root
-- cause SAMA dengan S3-41 (middleware.RequireRole sudah membolehkan GA di
-- level aplikasi sejak S3-41, tapi RLS di bawahnya belum tahu soal GA sama
-- sekali, jadi tetap menolak query-nya).
CREATE OR REPLACE FUNCTION prodo_is_group_admin_of_workspace(p_workspace_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM workspaces w
    WHERE w.id = p_workspace_id AND prodo_is_group_admin_of_org(w.org_id)
  )
$$;

DROP POLICY IF EXISTS wm_select ON workspace_members;
CREATE POLICY wm_select ON workspace_members
  FOR SELECT TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

DROP POLICY IF EXISTS wm_insert ON workspace_members;
CREATE POLICY wm_insert ON workspace_members
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

DROP POLICY IF EXISTS wm_update ON workspace_members;
CREATE POLICY wm_update ON workspace_members
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

DROP POLICY IF EXISTS wm_delete ON workspace_members;
CREATE POLICY wm_delete ON workspace_members
  FOR DELETE TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );
