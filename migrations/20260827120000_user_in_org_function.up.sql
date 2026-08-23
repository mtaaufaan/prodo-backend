-- S3-40 (implementation_gaps.md IG-14 lanjutan, pola sama migrasi
-- sebelumnya 20260827110000): SessionRepository.IsUserInOrg dipanggil dari
-- SessionHandler.ListForUser/RevokeAllForUser -- route ini SENGAJA TIDAK
-- pakai DBContextMiddleware (SessionRepository menganut pool langsung,
-- bukan db.Executor, karena user_sessions sendiri bukan tabel tenant-scoped
-- ber-RLS). Tapi begitu workspace_members/workspaces kena RLS (S3-42),
-- JOIN langsung ke keduanya dari pool prodo_app_user TANPA session
-- variable akan diam-diam kena filter RLS (0 baris, bukan error) --
-- IsUserInOrg SELALU false, GA tidak pernah lolos S3-39/40 walau memang
-- berwenang.
CREATE OR REPLACE FUNCTION prodo_user_in_org(p_user_id UUID, p_org_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM workspace_members wm
    JOIN workspaces w ON w.id = wm.workspace_id
    WHERE wm.user_id = p_user_id AND w.org_id = p_org_id
  )
$$;
