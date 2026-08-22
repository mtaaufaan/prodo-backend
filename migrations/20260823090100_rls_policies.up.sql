-- S2-10: RLS untuk tabel yang SUDAH ADA dan SUDAH DIQUERY app code di S2
-- (workspace_members, notifications, audit_logs). Tabel lain di
-- RLS_DESIGN.md §7 (organizations, workspaces, projects, tasks, dst) BELUM
-- di-enable RLS di sini -- belum ada satupun handler yang query tabel itu
-- secara langsung (organizations/workspaces cuma FK prerequisite dari
-- IG-09; projects/tasks/dst belum dibuat sampai S4+), jadi enable RLS
-- untuknya sekarang tidak menguji apa-apa dan hanya menambah kompleksitas
-- tanpa manfaat -- akan ditambahkan begitu handler pertamanya dibangun.
-- Lihat implementation_gaps.md IG-10 untuk detail dan rencana lanjutan.
--
-- Helper function di sini SENGAJA subset dari RLS_DESIGN.md §6 -- hanya
-- yang benar-benar dipakai policy di bawah (prodo_current_org_id,
-- prodo_is_group_admin, prodo_org_ids_array, dst BELUM dibuat karena belum
-- ada policy yang butuh org_id/group_id context -- lihat IG-10).

CREATE OR REPLACE FUNCTION prodo_current_user_id()
RETURNS UUID LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT NULLIF(current_setting('app.current_user_id', true), '')::UUID
$$;

CREATE OR REPLACE FUNCTION prodo_platform_role()
RETURNS TEXT LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT COALESCE(current_setting('app.current_platform_role', true), '')
$$;

CREATE OR REPLACE FUNCTION prodo_is_platform_admin()
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT prodo_platform_role() = 'platform_admin'
$$;

-- SECURITY DEFINER supaya tidak kena RLS rekursif saat baca
-- workspace_members dari dalam policy tabel lain (RLS_DESIGN.md §6 catatan
-- kaki).
CREATE OR REPLACE FUNCTION prodo_is_workspace_member(p_workspace_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM workspace_members
    WHERE workspace_id = p_workspace_id
      AND user_id = prodo_current_user_id()
  )
$$;

-- ============================================================
-- workspace_members (RLS_DESIGN.md §7.3)
-- Beda dari desain asli: klausa "workspace_id IN (SELECT id FROM
-- workspaces WHERE org_id = prodo_current_org_id())" DIHILANGKAN --
-- app.current_org_id tidak tersedia (IG-10). Isolasi tenant untuk kolom
-- ini sudah cukup dijaga oleh app layer (RequireRole, S2-09); policy di
-- sini murni safety-net membership check, bukan org boundary.
-- ============================================================
ALTER TABLE workspace_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_members FORCE ROW LEVEL SECURITY;

CREATE POLICY wm_select ON workspace_members
  FOR SELECT TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

CREATE POLICY wm_insert ON workspace_members
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

CREATE POLICY wm_update ON workspace_members
  FOR UPDATE TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

CREATE POLICY wm_delete ON workspace_members
  FOR DELETE TO prodo_app
  USING (prodo_is_workspace_member(workspace_id) OR prodo_is_platform_admin());

-- ============================================================
-- notifications (RLS_DESIGN.md §7.11)
-- Beda dari desain asli: dipecah jadi 4 policy per operasi (bukan satu
-- FOR ALL) karena INSERT-nya BUKAN oleh/untuk actor sendiri -- notifikasi
-- role-changed diinsert oleh AW/GA (actor) UNTUK target member lain
-- (WorkspaceMemberRepository.AssignRole). Kalau WITH CHECK ikut mewajibkan
-- user_id = current_user_id() (default FOR ALL tanpa WITH CHECK eksplisit
-- di Postgres), insert lintas-user itu akan DITOLAK. Ini sesuai
-- "Ringkasan: Tabel RLS Coverage" (§akhir dokumen) yang memang menandai
-- kolom INSERT notifications sebagai "App" (bebas asal lewat role
-- prodo_app), bukan "Owner" -- kode contoh §7.11 sendiri usang di titik ini.
-- ============================================================
ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;

CREATE POLICY notifications_select_own ON notifications
  FOR SELECT TO prodo_app
  USING (user_id = prodo_current_user_id());

CREATE POLICY notifications_insert_app ON notifications
  FOR INSERT TO prodo_app
  WITH CHECK (true);

CREATE POLICY notifications_update_own ON notifications
  FOR UPDATE TO prodo_app
  USING (user_id = prodo_current_user_id());

CREATE POLICY notifications_delete_own ON notifications
  FOR DELETE TO prodo_app
  USING (user_id = prodo_current_user_id());

-- ============================================================
-- audit_logs (RLS_DESIGN.md §7.10)
-- Beda dari desain asli: WITH CHECK (org_id = prodo_current_org_id())
-- DIHILANGKAN -- org_id belum pernah diisi oleh INSERT manapun di app
-- code saat ini (account_repository.go/workspace_member_repository.go
-- cuma isi actor_id/actor_role/action/entity_type/entity_id/workspace_id),
-- jadi syarat itu akan menolak SEMUA insert (org_id selalu NULL, current_
-- org_id() juga NULL, NULL = NULL bukan true). Sesuai Ringkasan Tabel RLS
-- Coverage, INSERT audit_logs memang "App" (bebas lewat role prodo_app),
-- bukan org-scoped. TIDAK ada policy SELECT/UPDATE/DELETE sama sekali --
-- konsisten dengan desain asli (immutability + belum ada endpoint baca
-- audit trail sampai fitur GA/AW Audit Trail dibangun).
-- ============================================================
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_logs_insert_app ON audit_logs
  FOR INSERT TO prodo_app
  WITH CHECK (true);

-- audit_logs adalah PARTITIONED TABLE (PARTITION BY RANGE logged_at, lihat
-- 20260820150100_platform_invitations_audit_logs.up.sql). Terverifikasi
-- live: ENABLE/FORCE ROW LEVEL SECURITY di tabel induk TIDAK otomatis
-- berlaku ke partisi (relrowsecurity partisi tetap 'f' walau induknya
-- 't') -- setiap partisi adalah relation terpisah dan harus di-enable
-- eksplisit, walaupun kebijakan (CREATE POLICY) di induk otomatis
-- berlaku ke partisi begitu RLS-nya sendiri di-enable.
ALTER TABLE audit_logs_2025 ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2025 FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2026 ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2026 FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2027 ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2027 FORCE ROW LEVEL SECURITY;
