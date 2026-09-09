-- Task Management Core Phase 2 (US-017/017b, forward-pull -- lihat
-- implementation_gaps.md IG-46/IG-47). task_pic_phases (§5.18) DAN
-- pic_group_configs (§5.34) -- akses "ikut task/project induk" (RLS_DESIGN.md
-- §7 catatan tabel anak), reuse fungsi projects yang sudah ada, TIDAK ada
-- fungsi RLS baru -- sama pola task_assignees Phase 1.

CREATE TABLE task_pic_phases (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  status_id       UUID NOT NULL REFERENCES custom_statuses(id),
  user_id         UUID NOT NULL REFERENCES users(id),
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  acknowledged_at TIMESTAMPTZ,
  activated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deactivated_at  TIMESTAMPTZ,
  assigned_by     UUID REFERENCES users(id)
);

CREATE INDEX idx_task_pic_phases_task_id ON task_pic_phases (task_id);
CREATE INDEX idx_task_pic_phases_active ON task_pic_phases (task_id, is_active) WHERE is_active = TRUE;

ALTER TABLE task_pic_phases ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_pic_phases FORCE ROW LEVEL SECURITY;

CREATE POLICY task_pic_phases_all ON task_pic_phases
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_pic_phases.task_id
        AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

-- ============================================================
-- pic_group_configs (§5.34) -- daftar user yang boleh dipilih sebagai PIC
-- untuk (project_id, status_id) tertentu, dipakai mode "Terbatas"
-- (Editor/Approver). Kosong = mode "Bebas" (PM/AW pilih siapa saja).
-- ============================================================
CREATE TABLE pic_group_configs (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  status_id  UUID NOT NULL REFERENCES custom_statuses(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  added_by   UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, status_id, user_id)
);

ALTER TABLE pic_group_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE pic_group_configs FORCE ROW LEVEL SECURITY;

CREATE POLICY pic_group_configs_all ON pic_group_configs
  FOR ALL TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR prodo_is_group_admin_of_project(project_id)
    OR prodo_is_workspace_member_of_project(project_id)
    OR prodo_is_project_member(project_id)
  );
