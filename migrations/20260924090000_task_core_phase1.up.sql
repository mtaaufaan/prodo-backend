-- Task Management Core Phase 1 (US-012 sisa, US-013, US-014 -- forward-pull
-- atas permintaan user untuk mengisi data nyata Performance Dashboard
-- S4G-25/26). sprint_backlog.md S4-06..21 menyebut nama tabel/kolom yang
-- SUDAH USANG dibanding DATABASE_SCHEMA.md (§5.11/5.14/5.15/5.16) --
-- migrasi ini mengikuti DATABASE_SCHEMA.md, BUKAN teks backlog:
--   task_status (backlog) -> custom_statuses (schema, polymorphic scope)
--   account_id/role (backlog task_assignees) -> user_id/assignee_role (schema)
--   sprints.status ENUM (backlog) -> sprints.is_active BOOLEAN (schema)
-- Phase 2 (PIC Handoff, task_pic_phases/pic_group_configs), Phase 3
-- (task_dependencies), Phase 4 (story point enforcement/task_status_sessions)
-- menyusul migrasi terpisah -- kolom story_points/completeness/task_code
-- SUDAH disertakan di sini (§5.15 penuh) sesuai "build forms complete
-- upfront", walau enforcement penuhnya baru Phase 3/4.

CREATE TYPE task_priority AS ENUM ('critical', 'high', 'medium', 'low');
CREATE TYPE task_completeness AS ENUM ('complete', 'incomplete');

-- ============================================================
-- custom_statuses (§5.11) -- polymorphic scope_type/scope_id, TIDAK bisa
-- FK biasa (lihat catatan schema), integritas dijaga application layer.
-- ============================================================
CREATE TABLE custom_statuses (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  scope_type   VARCHAR(20) NOT NULL CHECK (scope_type IN ('workspace', 'project')),
  scope_id     UUID NOT NULL,
  name         VARCHAR(100) NOT NULL,
  color_token  VARCHAR(50),
  position     SMALLINT NOT NULL DEFAULT 0,
  is_system    BOOLEAN NOT NULL DEFAULT FALSE,
  is_undefined BOOLEAN NOT NULL DEFAULT FALSE,
  created_by   UUID REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_custom_statuses_scope ON custom_statuses (scope_type, scope_id);

-- Seed 5 status sistem untuk SETIAP workspace yang SUDAH ADA -- alur
-- create-workspace baru diperbarui di WorkspaceRepository.Create supaya
-- workspace berikutnya ikut ter-seed otomatis.
INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system)
SELECT 'workspace', w.id, s.name, s.color_token, s.position, TRUE
FROM workspaces w
CROSS JOIN (VALUES
  ('BACKLOG', 'grey', 0),
  ('IN PROGRESS', 'accent', 1),
  ('UNDER REVIEW', 'violet', 2),
  ('DONE', 'mint', 3),
  ('BLOCKED', 'red', 4)
) AS s(name, color_token, position);

ALTER TABLE custom_statuses ENABLE ROW LEVEL SECURITY;
ALTER TABLE custom_statuses FORCE ROW LEVEL SECURITY;

-- SELECT/INSERT/UPDATE sama untuk kedua scope_type -- workspace member
-- (langsung, via prodo_is_workspace_member) ATAU project member (via
-- prodo_is_project_member -- fungsi sudah ada, dibuat migrasi projects).
-- DELETE tidak ada -- status "dihapus" berarti is_undefined=TRUE (UPDATE).
CREATE POLICY custom_statuses_select ON custom_statuses
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

CREATE POLICY custom_statuses_insert ON custom_statuses
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

CREATE POLICY custom_statuses_update ON custom_statuses
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

-- ============================================================
-- sprints (§5.14) -- selalu project-scoped, RLS reuse fungsi projects.
-- ============================================================
CREATE TABLE sprints (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       VARCHAR(255) NOT NULL,
  start_date DATE,
  end_date   DATE,
  is_active  BOOLEAN NOT NULL DEFAULT FALSE,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);

CREATE INDEX idx_sprints_project_id ON sprints (project_id);

ALTER TABLE sprints ENABLE ROW LEVEL SECURITY;
ALTER TABLE sprints FORCE ROW LEVEL SECURITY;

CREATE POLICY sprints_select ON sprints
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY sprints_insert ON sprints
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY sprints_update ON sprints
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY sprints_delete ON sprints
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id));

-- ============================================================
-- tasks (§5.15) -- tabel inti. RLS adaptasi §7.6 RLS_DESIGN.md dengan
-- KOREKSI yang sama seperti projects (migrations/20260829100000, IG-10/11):
-- reuse prodo_is_group_admin_of_project/prodo_is_workspace_member_of_project/
-- prodo_is_project_member yang SUDAH ADA -- bukan session-variable tunggal
-- prodo_current_org_id()/prodo_is_group_admin() draf asli §7.6 yang sudah
-- terbukti salah untuk model many-to-many grup/project-scoped member.
-- ============================================================
CREATE TABLE tasks (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  sprint_id       UUID REFERENCES sprints(id) ON DELETE SET NULL,
  parent_task_id  UUID REFERENCES tasks(id) ON DELETE CASCADE,
  status_id       UUID NOT NULL REFERENCES custom_statuses(id),
  title           VARCHAR(512) NOT NULL CHECK (char_length(title) >= 3),
  description     JSONB,
  priority        task_priority NOT NULL DEFAULT 'medium',
  completeness    task_completeness,
  due_date        DATE,
  estimated_hours NUMERIC(6,2) CHECK (estimated_hours IS NULL OR estimated_hours > 0),
  story_points    SMALLINT CHECK (story_points IS NULL OR story_points IN (1, 2, 3, 5, 8, 13)),
  task_code       VARCHAR(20),
  created_by      UUID NOT NULL REFERENCES users(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at    TIMESTAMPTZ,
  deleted_at      TIMESTAMPTZ,
  CHECK (parent_task_id != id)
);

CREATE INDEX idx_tasks_project_id ON tasks (project_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_sprint_id ON tasks (sprint_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_status_id ON tasks (status_id);
CREATE INDEX idx_tasks_parent_task_id ON tasks (parent_task_id) WHERE parent_task_id IS NOT NULL;

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;

CREATE POLICY tasks_select ON tasks
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY tasks_insert ON tasks
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

CREATE POLICY tasks_update ON tasks
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(project_id) OR prodo_is_workspace_member_of_project(project_id) OR prodo_is_project_member(project_id));

-- DELETE SQL sengaja dikosongkan (RLS_DESIGN.md §7.6) -- soft-delete lewat
-- UPDATE deleted_at, tercakup policy tasks_update (sama pola IG-43
-- workspaces/projects, ACCEPTED RISK defense-in-depth, bukan re-litigasi).

-- ============================================================
-- task_assignees (§5.16) -- multi-assignee paralel, akses ikut task induk.
-- ============================================================
CREATE TABLE task_assignees (
  task_id       UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  assignee_role VARCHAR(50) NOT NULL DEFAULT 'contributor' CHECK (assignee_role IN ('lead', 'contributor')),
  assigned_by   UUID REFERENCES users(id),
  assigned_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (task_id, user_id)
);

ALTER TABLE task_assignees ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_assignees FORCE ROW LEVEL SECURITY;

-- Ikut akses task induk (RLS_DESIGN.md §7 catatan tabel anak) -- subquery
-- ke project_id lewat tasks, reuse fungsi yang sama.
CREATE POLICY task_assignees_all ON task_assignees
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_assignees.task_id
        AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
