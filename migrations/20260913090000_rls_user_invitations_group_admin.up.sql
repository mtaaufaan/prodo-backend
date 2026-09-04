-- S4G-05, Track S4G (implementation_gaps.md IG-38): user_invitations RLS
-- (S2-19, migrations/20260825090000_rls_user_invitations.up.sql) tidak
-- pernah punya jalur Group Admin sama sekali -- cuma prodo_is_workspace_member
-- OR prodo_is_platform_admin(). Root cause SAMA PERSIS dengan IG-01/S3-41
-- (workspace_members, lihat migrations/20260827100000_rls_workspace_members_
-- group_admin.up.sql): GA yang BARU membuat workspace (WorkspaceService.
-- CreateWorkspace jalur "UNDANG BARU") belum jadi workspace_members di situ,
-- jadi INSERT undangan admin_workspace pertamanya DITOLAK RLS. Reuse penuh
-- fungsi prodo_is_group_admin_of_workspace yang sudah ada dari perbaikan
-- IG-01 -- tidak perlu fungsi baru.
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
