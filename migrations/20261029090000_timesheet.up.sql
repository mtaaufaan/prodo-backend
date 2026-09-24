-- Timesheet (DATABASE_SCHEMA.md §5.31/5.32) -- terdokumentasi sejak awal
-- (skema + API_CONTRACT.md §15) tapi TIDAK PERNAH dimigrasikan/dibangun.
-- Dimajukan dari Sprint S8 asli (26 SP) ke Track S5, IG-97 -- dikonfirmasi
-- user "dibangun lengkap, majukan saja tasknya agar sekalian selesai"
-- (chip header PM Task Detail.dc.html LOGGED/ESTIMASI JAM butuh data
-- jam ter-log yang nyata, bukan mock).
CREATE TYPE time_entry_type AS ENUM ('timer', 'manual');

CREATE TABLE time_entries (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id          UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users(id),
  started_at       TIMESTAMPTZ NOT NULL,
  ended_at         TIMESTAMPTZ,
  duration_minutes SMALLINT,
  entry_type       time_entry_type NOT NULL,
  is_approved      BOOLEAN,
  rejection_note   TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT ck_time_entry_dates CHECK (ended_at IS NULL OR ended_at > started_at),
  CONSTRAINT ck_time_entry_duration CHECK (duration_minutes IS NULL OR duration_minutes > 0),
  CONSTRAINT ck_rejection_note CHECK (
    rejection_note IS NULL OR (is_approved = FALSE AND rejection_note <> '')
  )
);

CREATE TABLE active_timers (
  user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  task_id    UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_time_entries_task_id ON time_entries (task_id);
CREATE INDEX idx_time_entries_user_id ON time_entries (user_id, started_at DESC);
CREATE INDEX idx_time_entries_pending ON time_entries (user_id)
  WHERE is_approved IS NULL AND entry_type = 'manual';

-- RLS (RLS_DESIGN.md §7.14/7.15, diadaptasi ke helper prodo_* yang SUDAH
-- ADA di codebase ini -- bukan current_setting() mentah seperti draft awal
-- dokumen, sama pola tasks_select/sprints_select).
ALTER TABLE time_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE time_entries FORCE ROW LEVEL SECURITY;

CREATE POLICY time_entries_select ON time_entries
  FOR SELECT TO prodo_app
  USING (
    user_id = prodo_current_user_id()
    OR EXISTS (
      SELECT 1 FROM tasks t
      JOIN projects p ON p.id = t.project_id
      JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
      WHERE t.id = time_entries.task_id
        AND wm.user_id = prodo_current_user_id()
        AND wm.role IN ('admin_workspace', 'project_manager')
    )
    OR prodo_is_platform_admin()
  );

CREATE POLICY time_entries_insert ON time_entries
  FOR INSERT TO prodo_app
  WITH CHECK (
    user_id = prodo_current_user_id()
    AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = time_entries.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY time_entries_update ON time_entries
  FOR UPDATE TO prodo_app
  USING (
    user_id = prodo_current_user_id()
    OR EXISTS (
      SELECT 1 FROM tasks t
      JOIN projects p ON p.id = t.project_id
      JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
      WHERE t.id = time_entries.task_id
        AND wm.user_id = prodo_current_user_id()
        AND wm.role IN ('admin_workspace', 'project_manager')
    )
  );

CREATE POLICY time_entries_delete ON time_entries
  FOR DELETE TO prodo_app
  USING (user_id = prodo_current_user_id());

ALTER TABLE active_timers ENABLE ROW LEVEL SECURITY;
ALTER TABLE active_timers FORCE ROW LEVEL SECURITY;

CREATE POLICY timers_own ON active_timers
  FOR ALL TO prodo_app
  USING (user_id = prodo_current_user_id())
  WITH CHECK (user_id = prodo_current_user_id());
