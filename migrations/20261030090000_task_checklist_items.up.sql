-- task_checklist_items (SUB-TASK, "PM Task Detail.dc.html", IG-97 susulan
-- -- diminta user "kenapa sub-task belum ada?"). SENGAJA tabel baru
-- RINGAN, BUKAN reuse tabel `tasks` (tasks.parent_task_id) -- sub-task di
-- desain cuma checklist item (checkbox + judul, tanpa assignee/PIC/
-- status/dependency), memaksanya lewat mesin Task penuh (assignee wajib,
-- PIC per pindah status, dsb) tidak proporsional untuk interaksi
-- checklist 2 detik yang diminta desain.
CREATE TABLE task_checklist_items (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id    UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  title      VARCHAR(300) NOT NULL,
  is_done    BOOLEAN NOT NULL DEFAULT FALSE,
  position   DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_task_checklist_items_task_id ON task_checklist_items (task_id, position);

-- RLS -- siapa pun yang boleh lihat/tulis task berarti boleh lihat/tulis
-- checklist item-nya, pola sama task_version_snapshots (IG-97).
ALTER TABLE task_checklist_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_checklist_items FORCE ROW LEVEL SECURITY;

CREATE POLICY task_checklist_items_select ON task_checklist_items
  FOR SELECT TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_checklist_items.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_checklist_items_write ON task_checklist_items
  FOR ALL TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_checklist_items.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_checklist_items.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
