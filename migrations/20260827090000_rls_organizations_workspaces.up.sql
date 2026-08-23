-- S3-42 (implementation_gaps.md IG-01/IG-10/IG-11/IG-13): RLS untuk
-- organizations+workspaces, tabel pertama yang benar-benar dikerjakan sejak
-- IG-10 mencatat baru 4/19 tabel (workspace_members/notifications/
-- audit_logs/user_invitations) yang ber-RLS.
--
-- ⚠️ BERBEDA dari RLS_DESIGN.md §7.1/7.2 versi tertulis dalam 2 hal
-- fundamental, keduanya ditemukan saat implementasi nyata (bukan cuma
-- koreksi kecil seperti IG-11):
--
-- 1. **Tidak pakai app.current_org_id/app.current_group_id sama sekali.**
--    RLS_DESIGN.md §7 menulis kondisi seperti `group_id =
--    prodo_current_group_id()` -- ini mengasumsikan SATU Group Admin cuma
--    mengelola SATU grup per sesi. Tapi group_admin_assignments (S3-38)
--    sengaja many-to-many (satu GA bisa kelola BANYAK grup/org sekaligus,
--    lihat DATABASE_SCHEMA.md §5.6) -- satu session variable string tidak
--    bisa merepresentasikan itu. Solusinya: helper function baru
--    prodo_is_group_admin_of_org(org_id)/prodo_is_group_admin_of_group(
--    group_id) yang QUERY LANGSUNG ke group_admin_assignments tiap kali
--    dipanggil (SECURITY DEFINER, sama pola prodo_is_workspace_member) --
--    tidak butuh session variable org/group SAMA SEKALI, selalu fresh dari
--    tabel sumber, tidak ada resiko snapshot basi.
-- 2. **Ditambahkan bypass Platform Admin eksplisit** (`prodo_is_platform_
--    admin()`) di SEMUA operasi -- RLS_DESIGN.md §7.1 menulis "Akses PA ke
--    organizations ditangani di application layer, bukan via RLS", tapi
--    implementasi nyata (OrganizationService.authorizeGroup/authorizeOrg,
--    S3-02/03/04/05/06) memang mengizinkan PA lewat kode aplikasi yang
--    SAMA dengan GA (query lewat prodo_app_user, bukan superuser) -- tanpa
--    bypass RLS eksplisit, PA akan diblokir RLS walau application layer
--    sudah mengizinkan (4-layer defense harus konsisten, bukan saling
--    kontradiksi). Ini juga menutup **IG-13** (organizations tidak punya
--    policy INSERT untuk prodo_app sama sekali di draf asli -- S3-02 GA
--    based create org akan gagal begitu RLS aktif tanpa fix ini).
--
-- Cabang project-scoped cross-org (RLS_DESIGN.md §7.2, akses lintas org
-- lewat project_members) SENGAJA BELUM ditambahkan -- tabel project_members
-- belum ada sampai S3-19 (US-009b), menambahkannya sekarang akan membuat
-- migration gagal (relasi ke tabel yang tidak ada). Ditambahkan begitu
-- S3-19 selesai -- dicatat sebagai IG-10 lanjutan.
--
-- 3. **Policy SELECT/UPDATE/DELETE `organizations` pakai
--    prodo_is_group_admin_of_group(group_id) -- kolom row itu sendiri --
--    BUKAN prodo_is_group_admin_of_org(id) (join balik ke organizations).**
--    Ditemukan lewat live-test: `INSERT ... RETURNING` (dipakai
--    OrganizationRepository.Create) mem-verify baris hasil INSERT terhadap
--    policy SELECT juga (perilaku standar Postgres RLS untuk RETURNING).
--    prodo_is_group_admin_of_org(id) melakukan self-join balik ke tabel
--    organizations untuk resolve group_id dari id -- baris yang BARU SAJA
--    di-INSERT oleh COMMAND YANG SAMA belum terlihat oleh subquery di
--    dalam command itu sendiri (Postgres command-counter visibility,
--    bukan bug RLS) -- SELECT check gagal, INSERT ditolak walau WITH
--    CHECK-nya sendiri (yang TIDAK self-join, cuma cek group_id langsung)
--    lolos. Fix: SELECT/UPDATE/DELETE organizations cukup pakai
--    prodo_is_group_admin_of_group(group_id) langsung dari kolom row itu
--    sendiri -- tidak perlu join balik sama sekali, jadi tidak kena
--    masalah visibility ini. prodo_is_group_admin_of_org(org_id) tetap
--    dipakai apa adanya di policy `workspaces` (join ke organizations,
--    TABEL LAIN yang tidak sedang di-INSERT -- aman, sudah committed/
--    visible).

CREATE OR REPLACE FUNCTION prodo_is_group_admin_of_org(p_org_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM group_admin_assignments gaa
    JOIN organizations o ON o.group_id = gaa.group_id
    WHERE gaa.user_id = prodo_current_user_id()
      AND o.id = p_org_id
  )
$$;

CREATE OR REPLACE FUNCTION prodo_is_group_admin_of_group(p_group_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM group_admin_assignments gaa
    WHERE gaa.user_id = prodo_current_user_id()
      AND gaa.group_id = p_group_id
  )
$$;

-- ============================================================
-- organizations (RLS_DESIGN.md §7.1, terkoreksi -- lihat catatan di atas)
-- ============================================================
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE ROW LEVEL SECURITY;

CREATE POLICY orgs_select ON organizations
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_group(group_id)
    OR (
      prodo_platform_role() = 'member'
      AND EXISTS (
        SELECT 1 FROM workspace_members wm
        JOIN workspaces w ON w.id = wm.workspace_id
        WHERE w.org_id = organizations.id AND wm.user_id = prodo_current_user_id()
      )
    )
  );

-- IG-13: policy INSERT yang sebelumnya tidak ada sama sekali.
CREATE POLICY orgs_insert ON organizations
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_group(group_id)
  );

CREATE POLICY orgs_update ON organizations
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

-- DELETE eksplisit ditambahkan -- draf asli §7.1 juga tidak punya ini
-- ("INSERT dan DELETE: hanya via prodo_migrator"), tapi S3-05
-- (DELETE /organizations/:id) berjalan lewat prodo_app_user sama seperti
-- endpoint lain, sama root cause dengan IG-13.
CREATE POLICY orgs_delete ON organizations
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

-- ============================================================
-- workspaces (RLS_DESIGN.md §7.2, terkoreksi -- lihat catatan di atas)
-- ============================================================
ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;

CREATE POLICY workspaces_select ON workspaces
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_org(org_id)
    OR prodo_is_workspace_member(id)
    -- Cabang project-scoped cross-org (project_members) menyusul S3-19.
  );

CREATE POLICY workspaces_insert ON workspaces
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_org(org_id));

CREATE POLICY workspaces_update ON workspaces
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_org(org_id)
    OR prodo_is_workspace_member(id)
  );

CREATE POLICY workspaces_delete ON workspaces
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_org(org_id));
