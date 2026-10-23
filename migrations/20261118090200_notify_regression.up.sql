-- Notifikasi regresi task dijalankan di fungsi SECURITY DEFINER (S7A-13, implementation_gaps.md IG-159).
--
-- Temuan probe live Sprint 7 (pola sama prodo_notify_approval_pending, migrasi 20261118090100): NotifyRegression
-- meng-INSERT...SELECT penerima (project_managers + Admin Workspace) di sesi PELAKU. Pelaku project-scoped (mis.
-- Editor tanpa baris workspace_members) hanya melihat baris keanggotaannya sendiri lewat RLS (pm_select,
-- workspace_members), sehingga PM dan Admin Workspace tidak pernah dinotifikasi saat task mundur status.
-- Perilaku bagi pelaku lain tidak berubah: penerima = seluruh PM project + seluruh Admin Workspace workspace-nya.
CREATE OR REPLACE FUNCTION prodo_notify_regression(p_task_id UUID)
RETURNS INTEGER LANGUAGE plpgsql VOLATILE SECURITY DEFINER AS $$
DECLARE
  v_count INTEGER;
BEGIN
  INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
  SELECT DISTINCT rcp.user_id, NULL::uuid, 'task_regressed', 'task', t.id,
         'Task Mengalami Regresi', 'Sebuah task kembali ke status yang lebih awal dari sebelumnya.'
  FROM tasks t
  JOIN (
    SELECT pmg.user_id, pmg.project_id FROM project_managers pmg
    UNION
    SELECT wm.user_id, p.id FROM workspace_members wm
    JOIN projects p ON p.workspace_id = wm.workspace_id
    WHERE wm.role = 'admin_workspace'
  ) rcp ON rcp.project_id = t.project_id
  WHERE t.id = p_task_id;
  GET DIAGNOSTICS v_count = ROW_COUNT;
  RETURN v_count;
END;
$$;

REVOKE ALL ON FUNCTION prodo_notify_regression(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION prodo_notify_regression(UUID) TO prodo_app;
