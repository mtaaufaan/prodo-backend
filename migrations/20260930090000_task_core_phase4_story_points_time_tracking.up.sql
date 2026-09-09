-- Task Management Core Phase 4 (US-018a/018b/018c, forward-pull -- lihat
-- implementation_gaps.md IG-46/47/48/49). Kolom story_points SUDAH ADA
-- sejak Phase 1 (20260924090000) -- migrasi ini menambah: (1) gate
-- Editor-boleh-isi-SP per project, (2) toggle "butuh konfirmasi mulai"
-- per status, (3) task_status_sessions (DATABASE_SCHEMA.md §5.37) untuk
-- Queue/Active Time, Flow Efficiency, dan Regression Rate.

ALTER TABLE projects ADD COLUMN allow_editor_story_points BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE custom_statuses ADD COLUMN require_start_confirmation BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE task_status_sessions (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id          UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  status_id        UUID NOT NULL REFERENCES custom_statuses(id),
  session_no       SMALLINT NOT NULL,
  entered_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  work_started_at  TIMESTAMPTZ,
  is_auto_start    BOOLEAN NOT NULL DEFAULT FALSE,
  exited_at        TIMESTAMPTZ,
  is_regression    BOOLEAN NOT NULL DEFAULT FALSE,
  triggered_by     UUID REFERENCES users(id)
);

CREATE INDEX idx_task_status_sessions_task_id ON task_status_sessions (task_id, entered_at DESC);
CREATE INDEX idx_task_status_sessions_active ON task_status_sessions (task_id)
  WHERE exited_at IS NULL;
CREATE INDEX idx_task_status_sessions_regression ON task_status_sessions (task_id)
  WHERE is_regression = TRUE;
CREATE INDEX idx_task_status_sessions_status_id ON task_status_sessions (status_id);

-- RLS "ikut task induk", reuse fungsi projects yang sudah ada -- sama pola
-- task_pic_phases/task_dependencies (Phase 2/3), TIDAK ada fungsi RLS baru.
ALTER TABLE task_status_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_status_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY task_status_sessions_all ON task_status_sessions
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_status_sessions.task_id
        AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
