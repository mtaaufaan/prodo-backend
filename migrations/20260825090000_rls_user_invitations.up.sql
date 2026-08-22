-- S2-19/20/21/22: user_invitations mendapat handler pertamanya di task
-- ini -- wajib di-RLS di PR yang sama (definition-of-done.md §2.3,
-- ditambahkan setelah audit S2 H6 menemukan organizations/workspaces
-- sempat berjalan tanpa RLS beberapa sprint, implementation_gaps.md IG-10).
--
-- Beda dari RLS_DESIGN.md §7.16 (versi asli):
-- 1. Klausa `workspace_id IN (SELECT id FROM workspaces WHERE org_id =
--    prodo_current_org_id())` DIHILANGKAN -- app.current_org_id belum
--    tersedia (IG-10), sama pola dengan workspace_members S2-10.
-- 2. Policy UPDATE DITAMBAHKAN -- desain asli bilang "UPDATE tidak
--    diperlukan, accepted_at diisi prodo_migrator (superuser)". Realisasi
--    di sini SENGAJA BEDA: AcceptInvitation (rute publik, tanpa sesi)
--    memang lewat konteks tepercaya (session var platform_admin, BUKAN
--    superuser -- lihat internal/service/invitation.go komentar
--    AcceptInvitation), TAPI Cancel/Resend (S2-21/22) dieksekusi actor AW
--    biasa lewat sesi normalnya sendiri, BUKAN prodo_migrator -- keduanya
--    genuinely butuh policy UPDATE, bukan cuma bypass superuser.
ALTER TABLE user_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_invitations FORCE ROW LEVEL SECURITY;

-- SELECT: dipakai FindPendingByTokenHash (AcceptInvitation, platform_admin
-- bypass) -- juga disiapkan untuk fitur listing undangan workspace (S2-28)
-- yang belum dibangun.
CREATE POLICY invitations_select ON user_invitations
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_workspace_member(workspace_id)
  );

-- INSERT: AW membuat undangan di workspace mereka sendiri (CreateInvitation,
-- S2-17/19).
CREATE POLICY invitations_insert ON user_invitations
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
  );

-- UPDATE: AW mengubah undangan workspace mereka (Cancel/Resend, S2-21/22)
-- ATAU konteks tepercaya accept-flow (AcceptInvitation menandai
-- accepted_at, platform_admin bypass -- lihat catatan di atas).
CREATE POLICY invitations_update ON user_invitations
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_workspace_member(workspace_id)
    OR prodo_is_platform_admin()
  );

-- DELETE: TIDAK ADA POLICY SENGAJA -- baris undangan tidak pernah
-- benar-benar dihapus (DATABASE_SCHEMA.md §5.30, "cancelled_at diisi,
-- baris tidak dihapus"), default-deny RLS jadi safety net tambahan
-- di atas app layer yang memang tidak pernah memanggil DELETE SQL.
