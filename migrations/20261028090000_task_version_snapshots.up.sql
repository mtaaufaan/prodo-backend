-- task_version_snapshots (DATABASE_SCHEMA.md §5.19) -- terdokumentasi sejak
-- awal tapi TIDAK PERNAH dimigrasikan ("tabel hantu") sampai IG-97 (RIWAYAT
-- VERSI, TaskDetailModal). `trigger` kolom TAMBAHAN di luar §5.19 asli --
-- lihat komentar TaskVersionSnapshot (task_repository.go).
CREATE TABLE task_version_snapshots (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  title       VARCHAR(512) NOT NULL,
  description JSONB,
  changed_by  UUID NOT NULL REFERENCES users(id),
  trigger     VARCHAR(100) NOT NULL,
  snapshot_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_task_version_snapshots_task_id ON task_version_snapshots (task_id, snapshot_at DESC);

-- RLS: siapa pun yang boleh lihat task berarti boleh lihat riwayat
-- versinya (bukan data sensitif tambahan, snapshot deskripsi yang sama
-- persis boleh dilihat lewat tab RINGKASAN) -- pola sama prodo_is_project_member
-- dkk yang sudah dipakai tasks_select (20260924090000_task_core_phase1).
ALTER TABLE task_version_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_version_snapshots FORCE ROW LEVEL SECURITY;

CREATE POLICY task_version_snapshots_select ON task_version_snapshots
  FOR SELECT TO prodo_app
  USING (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_version_snapshots.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );

CREATE POLICY task_version_snapshots_insert ON task_version_snapshots
  FOR INSERT TO prodo_app
  WITH CHECK (
    EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = task_version_snapshots.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id)
             OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
    )
  );
