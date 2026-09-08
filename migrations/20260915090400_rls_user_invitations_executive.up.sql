-- Gap yang sama pola IG-38: kolom baru (is_executive_invite/group_id, lihat
-- 20260915090300) TIDAK dijangkau policy RLS user_invitations yang ada --
-- ketiganya cuma cek prodo_is_workspace_member(workspace_id)/prodo_is_
-- group_admin_of_workspace(workspace_id), yang SELALU false kalau
-- workspace_id NULL (kasus undangan eksekutif). Tanpa fix ini, GA membuat
-- undangan eksekutif murni akan gagal RLS (WITH CHECK) sebelum sempat
-- coba INSERT sungguhan.
DROP POLICY invitations_insert ON user_invitations;
CREATE POLICY invitations_insert ON user_invitations
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR prodo_is_workspace_member(workspace_id)
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR (is_executive_invite AND prodo_is_group_admin_of_group(group_id))
  );

DROP POLICY invitations_select ON user_invitations;
CREATE POLICY invitations_select ON user_invitations
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_workspace_member(workspace_id)
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR (is_executive_invite AND prodo_is_group_admin_of_group(group_id))
  );

DROP POLICY invitations_update ON user_invitations;
CREATE POLICY invitations_update ON user_invitations
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR (is_executive_invite AND prodo_is_group_admin_of_group(group_id))
  );
