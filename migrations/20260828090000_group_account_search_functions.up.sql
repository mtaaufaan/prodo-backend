-- S3-20 (US-009b): GET /groups/:groupId/accounts/search -- PM cari user
-- lintas org DALAM GRUP YANG SAMA. Butuh fungsi SECURITY DEFINER (bukan
-- query RLS-aware biasa) karena request datangnya justru dari user yang
-- BUKAN group_admin/org member di semua org tujuan -- seorang Project
-- Manager di Org A harus bisa melihat user di Org B (grup sama) untuk
-- ditambahkan sebagai project-scoped member (S3-21) nanti, tapi RLS
-- `organizations`/`workspaces`/`workspace_members` (S3-42) SENGAJA
-- membatasi visibility PM ke org-nya sendiri saja -- pola sama
-- prodo_group_admin_org_ids/prodo_user_in_org (IG-14) yang juga butuh
-- bypass RLS untuk kasus lintas-tenant yang disengaja, bukan bug.
CREATE OR REPLACE FUNCTION prodo_is_project_manager_in_group(p_group_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1
    FROM workspace_members wm
    JOIN workspaces w ON w.id = wm.workspace_id
    JOIN organizations o ON o.id = w.org_id
    WHERE wm.user_id = prodo_current_user_id()
      AND wm.role = 'project_manager'
      AND o.group_id = p_group_id
  )
$$;

CREATE OR REPLACE FUNCTION prodo_search_accounts_in_group(p_group_id UUID, p_query TEXT)
RETURNS TABLE (user_id UUID, email VARCHAR, display_name VARCHAR, org_id UUID, org_name VARCHAR)
LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT DISTINCT u.id, u.email, u.display_name, o.id, o.name
  FROM users u
  JOIN workspace_members wm ON wm.user_id = u.id
  JOIN workspaces w ON w.id = wm.workspace_id
  JOIN organizations o ON o.id = w.org_id
  WHERE o.group_id = p_group_id
    AND (p_query = '' OR u.display_name ILIKE '%' || p_query || '%' OR u.email ILIKE '%' || p_query || '%')
  ORDER BY u.display_name
  LIMIT 50
$$;
