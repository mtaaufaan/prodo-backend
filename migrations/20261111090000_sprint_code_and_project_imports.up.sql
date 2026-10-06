-- Import CSV PM tahap (a) -- kode sprint + import sprint (IG-120).
-- 1) sprints.code: kunci penghubung import task ke sprint. Unik per project
--    (case-insensitive). Sprint yang sudah ada diberi SPR-01, SPR-02, ...
--    urut tanggal dibuat per project.
ALTER TABLE sprints ADD COLUMN code VARCHAR(20);

UPDATE sprints s
SET code = 'SPR-' || CASE WHEN x.rn < 10 THEN '0' || x.rn::text ELSE x.rn::text END
FROM (
  SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY created_at, id) AS rn FROM sprints
) x
WHERE x.id = s.id;

ALTER TABLE sprints ALTER COLUMN code SET NOT NULL;
CREATE UNIQUE INDEX idx_sprints_project_code ON sprints (project_id, upper(code));

-- 2) project_imports: riwayat import level PROJECT (dikelola PM / Admin
--    Workspace). Terpisah dari csv_imports (level group, kind member) --
--    scope, otorisasi, dan RLS-nya berbeda. Eksekusi sinkron di request,
--    jadi tidak ada status 'running'.
CREATE TABLE project_imports (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  project_id        UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  kind              VARCHAR(10) NOT NULL,
  imported_by       UUID NOT NULL REFERENCES users(id),
  actor_role        VARCHAR(30) NOT NULL,
  original_filename VARCHAR(512) NOT NULL,
  status            VARCHAR(12) NOT NULL DEFAULT 'pending',
  total_rows        INTEGER NOT NULL DEFAULT 0,
  success_count     INTEGER,
  failed_count      INTEGER,
  row_results       JSONB NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at      TIMESTAMPTZ,
  CONSTRAINT chk_project_imports_kind CHECK (kind IN ('sprint', 'task')),
  CONSTRAINT chk_project_imports_status CHECK (status IN ('pending', 'completed', 'failed'))
);

CREATE INDEX idx_project_imports_project ON project_imports (project_id, created_at DESC);

ALTER TABLE project_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_imports FORCE ROW LEVEL SECURITY;

-- RLS = isolasi keanggotaan; gate ROLE (PM/Admin Workspace) di application layer.
CREATE POLICY project_imports_select ON project_imports
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id)
         OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY project_imports_insert ON project_imports
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id)
              OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY project_imports_update ON project_imports
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id)
         OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));
