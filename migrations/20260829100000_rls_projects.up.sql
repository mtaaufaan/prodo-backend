-- RLS untuk projects+project_members (RLS_DESIGN.md §7.4/7.5), ditulis
-- SEGERA setelah tabelnya ada (forward-pull 20260829090000) -- pola sama
-- organizations/workspaces (S3-42): tabel tenant-scoped baru langsung
-- dapat RLS di migrasi yang sama/berdekatan, bukan ditunda.
--
-- ⚠️ Adaptasi dari RLS_DESIGN.md §7.4/7.5 tertulis, BUKAN salinan
-- verbatim -- draf asli pakai prodo_current_org_id()/prodo_is_group_admin()
-- (session variable tunggal) yang SUDAH DIKOREKSI sejak S3-42 (IG-10/IG-11):
-- helper query LANGSUNG ke tabel sumber (group_admin_assignments/
-- workspace_members), bukan session variable, karena GA bisa kelola BANYAK
-- org/grup dan model many-to-many tidak muat di satu variable.
--
-- ⚠️ Pelajaran IG-11 (boolean struktur) diterapkan: cabang akses LINTAS ORG
-- (project-scoped member via project_members) di-OR di level PALING LUAR,
-- TERPISAH dari cabang org-bound (workspace member/GA) -- BUKAN di-AND
-- bersama syarat org/workspace seperti draf awal RLS_DESIGN.md §7 yang
-- pernah salah (lihat catatan koreksi di RLS_DESIGN.md sendiri).

CREATE OR REPLACE FUNCTION prodo_is_group_admin_of_project(p_project_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = p_project_id AND prodo_is_group_admin_of_workspace(p.workspace_id)
  )
$$;

CREATE OR REPLACE FUNCTION prodo_is_workspace_member_of_project(p_project_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = p_project_id AND prodo_is_workspace_member(p.workspace_id)
  )
$$;

-- SECURITY DEFINER, sama pola prodo_is_workspace_member -- dipakai policy
-- projects_select untuk cabang cross-org (baris ini SENDIRI adalah
-- project_members, jadi tidak ada masalah command-counter visibility
-- IG-14 di sini -- fungsi ini dipanggil DARI policy tabel `projects`,
-- bukan dari INSERT ... RETURNING ke project_members itu sendiri).
CREATE OR REPLACE FUNCTION prodo_is_project_member(p_project_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1 FROM project_members
    WHERE project_id = p_project_id AND user_id = prodo_current_user_id()
  )
$$;

-- ============================================================
-- projects (RLS_DESIGN.md §7.4, adaptasi)
-- ============================================================
ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects FORCE ROW LEVEL SECURITY;

CREATE POLICY projects_select ON projects
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR prodo_is_workspace_member(workspace_id)
    -- Cross-org project-scoped access -- OR di level terluar (IG-11).
    OR prodo_is_project_member(id)
  );

CREATE POLICY projects_insert ON projects
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR prodo_is_workspace_member(workspace_id)
  );

CREATE POLICY projects_update ON projects
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
    OR prodo_is_workspace_member(workspace_id)
  );

CREATE POLICY projects_delete ON projects
  FOR DELETE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_workspace(workspace_id)
  );

-- ============================================================
-- project_members (RLS_DESIGN.md §7.5, adaptasi)
-- ============================================================
ALTER TABLE project_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_members FORCE ROW LEVEL SECURITY;

CREATE POLICY pm_select ON project_members
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
    -- Self-visibility: project-scoped member (is_scoped=TRUE, bukan
    -- workspace member di workspace ini) tetap bisa lihat baris MILIK
    -- SENDIRI walau tidak lolos cabang manapun di atas.
    OR user_id = prodo_current_user_id()
  );

CREATE POLICY pm_insert ON project_members
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
  );

CREATE POLICY pm_update ON project_members
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
  );

CREATE POLICY pm_delete ON project_members
  FOR DELETE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
  );
