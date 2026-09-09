-- Task Management Core Phase 3 (US-017c/018, forward-pull -- lihat
-- implementation_gaps.md IG-46/47/48). Kolom completeness SUDAH ADA sejak
-- Phase 1 (20260924090000) -- migrasi ini CUMA menambah task_dependencies
-- (DATABASE_SCHEMA.md §5.17). RLS "ikut task induk", reuse fungsi projects
-- yang sudah ada -- sama pola task_pic_phases Phase 2, TIDAK ada fungsi
-- RLS baru.

CREATE TABLE task_dependencies (
  predecessor_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  successor_id   UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  created_by     UUID REFERENCES users(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (predecessor_id, successor_id),
  CHECK (predecessor_id != successor_id)
);

CREATE INDEX idx_task_dependencies_successor ON task_dependencies (successor_id);
CREATE INDEX idx_task_dependencies_predecessor ON task_dependencies (predecessor_id);

ALTER TABLE task_dependencies ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_dependencies FORCE ROW LEVEL SECURITY;

CREATE POLICY task_dependencies_all ON task_dependencies
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_dependencies.successor_id
        AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
