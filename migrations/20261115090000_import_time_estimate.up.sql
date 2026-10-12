-- Estimasi jam tercatat dari riwayat tanggal status hasil import CSV (IG-129).
-- tasks.is_imported: penanda task hasil import (sebelumnya hanya jejak di
-- audit_logs state_after.imported, tidak terbaca PM karena RLS audit) --
-- dipakai penghitung estimasi supaya task biasa tidak ikut diestimasi.
-- time_entries.is_estimate: entri disetujui hasil perhitungan (bukan jam yang
-- dicatat orangnya); diganti seluruhnya saat estimasi dihitung ulang.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS is_imported BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE time_entries ADD COLUMN IF NOT EXISTS is_estimate BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE tasks t SET is_imported = TRUE
WHERE EXISTS (
  SELECT 1 FROM audit_logs al
  WHERE al.entity_type = 'task' AND al.entity_id = t.id AND al.action = 'task.created'
    AND al.state_after->>'imported' = 'true'
);

CREATE INDEX IF NOT EXISTS idx_time_entries_estimate ON time_entries (task_id) WHERE is_estimate;

-- Entri estimasi dibuat/dihapus atas nama ORANG LAIN oleh PM/AW project
-- (kebijakan insert/delete bawaan hanya untuk pemilik entri).
CREATE POLICY time_entries_insert_estimate ON time_entries
  FOR INSERT TO prodo_app
  WITH CHECK (
    is_estimate AND is_approved = TRUE AND entry_type = 'manual'
    AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = time_entries.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id) OR prodo_is_project_member(t.project_id)
             OR EXISTS (SELECT 1 FROM projects p JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
                        WHERE p.id = t.project_id AND wm.user_id = prodo_current_user_id()
                          AND wm.role IN ('admin_workspace', 'project_manager')))
    )
  );

CREATE POLICY time_entries_delete_estimate ON time_entries
  FOR DELETE TO prodo_app
  USING (
    is_estimate
    AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.id = time_entries.task_id
        AND (prodo_is_platform_admin() OR prodo_is_group_admin_of_project(t.project_id) OR prodo_is_project_member(t.project_id)
             OR EXISTS (SELECT 1 FROM projects p JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
                        WHERE p.id = t.project_id AND wm.user_id = prodo_current_user_id()
                          AND wm.role IN ('admin_workspace', 'project_manager')))
    )
  );
