-- IG-100 susulan (desain "AW Invite Member.dc.html" actingRole='Project
-- Manager', pool "MEMBER TERDAFTAR DI LUAR WORKSPACE INI"). BEDA dari
-- prodo_search_accounts_in_group (S3-20, dibatasi SATU grup) -- pool ini
-- SENGAJA lintas SELURUH organisasi di sistem (dikonfirmasi user), karena
-- Project Manager boleh menambah project-scoped member dari organisasi
-- MANA PUN, bukan cuma grup yang sama. Butuh SECURITY DEFINER sama alasan
-- prodo_search_accounts_in_group: request datang dari PM yang bukan
-- member/admin di organisasi lain manapun, RLS `organizations`/
-- `workspaces` normal akan membatasi visibility ke org-nya sendiri.
--
-- Pending invitation (user_invitations, keyed by email bukan user_id)
-- dikecualikan juga -- orang yang sudah diundang ke workspace target tidak
-- perlu muncul lagi di pool "tambah member".
CREATE OR REPLACE FUNCTION prodo_project_member_candidates(p_workspace_id UUID)
RETURNS TABLE (user_id UUID, email VARCHAR, display_name VARCHAR, org_id UUID, org_name VARCHAR)
LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT DISTINCT u.id, u.email, u.display_name, o.id, o.name
  FROM users u
  JOIN workspace_members wm ON wm.user_id = u.id
  JOIN workspaces w ON w.id = wm.workspace_id
  JOIN organizations o ON o.id = w.org_id
  WHERE o.deleted_at IS NULL
    AND NOT EXISTS (
      SELECT 1 FROM workspace_members wm2
      WHERE wm2.workspace_id = p_workspace_id AND wm2.user_id = u.id
    )
    AND NOT EXISTS (
      SELECT 1 FROM user_invitations ui
      WHERE ui.workspace_id = p_workspace_id AND ui.email = u.email
        AND ui.accepted_at IS NULL AND ui.cancelled_at IS NULL
    )
  ORDER BY u.display_name
  LIMIT 50
$$;
