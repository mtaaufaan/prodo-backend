-- Fix: function SECURITY DEFINER (bypass RLS, pola PERSIS
-- prodo_group_admin_org_ids/migrasi 20260827110000) -- dipanggil
-- AccountRepository.RecordLogin (2026-09-11) untuk resolve grup mana saja
-- yang harus melihat baris audit login member ini. workspace_members/
-- workspaces/organizations SEMUA force RLS; RecordLogin berjalan SEBELUM
-- ada session RLS (chicken-egg sama seperti IG-14) -- JOIN langsung dari
-- kode aplikasi akan diam-diam mengembalikan 0 baris.
CREATE OR REPLACE FUNCTION prodo_member_group_ids(p_user_id UUID)
RETURNS SETOF UUID LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT DISTINCT o.group_id
  FROM workspace_members wm
  JOIN workspaces w ON w.id = wm.workspace_id
  JOIN organizations o ON o.id = w.org_id
  WHERE wm.user_id = p_user_id
$$;
