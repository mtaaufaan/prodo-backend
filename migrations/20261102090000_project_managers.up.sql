-- Multi-PM per project (dikonfirmasi user setelah menemukan "+ Tetapkan
-- PM" ternyata MENGGANTI PM yang ada, bukan menambah -- "bagaimana cara
-- menambah PM dalam suatu project?"). projects.pm_user_id (kolom tunggal,
-- 20260909090000_projects_code_pm_softdelete) diganti tabel relasi
-- many-to-many, pola PERSIS project_members (20260829090000) -- satu-
-- satunya beda: tidak ada kolom role/is_scoped, PM SELALU
-- workspace_role='project_manager' (bukan project_scoped_role), lihat
-- komentar migrasi asli kenapa PM sengaja tidak lewat project_members.
CREATE TABLE project_managers (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_by   UUID REFERENCES users(id),
  added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, user_id)
);

CREATE INDEX idx_project_managers_user_id ON project_managers (user_id);

INSERT INTO project_managers (project_id, user_id, added_at)
SELECT id, pm_user_id, updated_at FROM projects WHERE pm_user_id IS NOT NULL;

ALTER TABLE projects DROP COLUMN pm_user_id;

-- RLS -- pola PERSIS project_members (RLS_DESIGN.md §7.5 adaptasi, lihat
-- migrasi 20260829100000_rls_projects) -- prodo_is_group_admin_of_project/
-- prodo_is_workspace_member_of_project sudah ada sejak migrasi itu, reuse
-- langsung, tidak perlu fungsi SQL baru.
ALTER TABLE project_managers ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_managers FORCE ROW LEVEL SECURITY;

CREATE POLICY pmg_select ON project_managers
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
    OR user_id = prodo_current_user_id()
  );

CREATE POLICY pmg_insert ON project_managers
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
  );

CREATE POLICY pmg_delete ON project_managers
  FOR DELETE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
  );
