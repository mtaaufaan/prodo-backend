-- Fix: function SECURITY DEFINER (bypass RLS, pola PERSIS
-- prodo_member_group_ids/migrasi 20261014090000) -- dipanggil
-- AccountRepository.RecordLogin/logSelfAccountAudit (2026-09-21, IG-89)
-- untuk resolve workspace mana saja yang harus melihat baris audit
-- login/aksi self-service member ini. workspace_members force RLS;
-- RecordLogin berjalan SEBELUM ada session RLS (chicken-egg sama seperti
-- IG-14) -- JOIN langsung dari kode aplikasi akan diam-diam mengembalikan
-- 0 baris.
CREATE OR REPLACE FUNCTION prodo_member_workspace_ids(p_user_id UUID)
RETURNS SETOF UUID LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT DISTINCT workspace_id FROM workspace_members WHERE user_id = p_user_id
$$;
