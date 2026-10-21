-- Notifikasi `approval_pending` ke Approver saat task masuk UNDER REVIEW (S7A-12, implementation_gaps.md IG-158).
--
-- Temuan probe live: penentuan penerima dan penyaring duplikat TIDAK dapat berjalan di sesi pelaku. Pelaku Editor
-- project-scoped hanya melihat baris keanggotaannya sendiri (RLS pm_select/workspace_members) dan seluruh
-- `notifications` ber-RLS `user_id = pemanggil`, sehingga penerima tak terlihat dan pemeriksaan "sudah dinotifikasi"
-- selalu lolos. Seluruh logika karena itu dijalankan di fungsi SECURITY DEFINER (pola prodo_is_project_member*):
--   penerima = anggota project berperan approver, ATAU Approver workspace yang mengikuti project (anggota project,
--   assignee, atau PIC aktif), pengguna aktif (tidak dihapus/ditangguhkan), bukan pelaku;
--   satu kali per penerima per fase (sesi status terbuka) -- harus dipanggil SETELAH sesi UNDER REVIEW dibuka.
-- Mengembalikan jumlah notifikasi yang dibuat.
CREATE OR REPLACE FUNCTION prodo_notify_approval_pending(p_task_id UUID, p_actor_id UUID)
RETURNS INTEGER LANGUAGE plpgsql VOLATILE SECURITY DEFINER AS $$
DECLARE
  v_count INTEGER;
BEGIN
  INSERT INTO notifications (user_id, actor_id, type, entity_type, entity_id, title, body)
  SELECT rcp.user_id, p_actor_id, 'approval_pending', 'task', t.id,
         'Task menunggu keputusan Anda -- ' || COALESCE(t.task_code, ''),
         'Task "' || t.title || '" masuk UNDER REVIEW dan menunggu keputusan Anda.'
  FROM tasks t
  JOIN (
    SELECT pm.user_id FROM project_members pm
    JOIN tasks tp ON tp.project_id = pm.project_id
    WHERE tp.id = p_task_id AND pm.role = 'approver'
    UNION
    SELECT wm.user_id FROM workspace_members wm
    JOIN projects p ON p.workspace_id = wm.workspace_id
    JOIN tasks tp ON tp.project_id = p.id
    WHERE tp.id = p_task_id AND wm.role = 'approver' AND (
      EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = wm.user_id)
      OR EXISTS (SELECT 1 FROM tasks tk JOIN task_assignees ta ON ta.task_id = tk.id
                 WHERE tk.project_id = p.id AND tk.deleted_at IS NULL AND ta.user_id = wm.user_id)
      OR EXISTS (SELECT 1 FROM tasks tk JOIN task_pic_phases tpp ON tpp.task_id = tk.id
                 WHERE tk.project_id = p.id AND tk.deleted_at IS NULL AND tpp.user_id = wm.user_id AND tpp.is_active)
    )
  ) rcp ON TRUE
  JOIN users u ON u.id = rcp.user_id AND u.is_active AND u.deleted_at IS NULL AND u.suspended_at IS NULL
  WHERE t.id = p_task_id AND t.deleted_at IS NULL
    AND (p_actor_id IS NULL OR rcp.user_id <> p_actor_id)
    AND NOT EXISTS (
      SELECT 1 FROM notifications n
      WHERE n.user_id = rcp.user_id AND n.type = 'approval_pending' AND n.entity_id = t.id
        AND n.created_at >= COALESCE((SELECT MAX(tss.entered_at) FROM task_status_sessions tss
                                      WHERE tss.task_id = t.id AND tss.exited_at IS NULL), '-infinity'::timestamptz)
    );
  GET DIAGNOSTICS v_count = ROW_COUNT;
  RETURN v_count;
END;
$$;

REVOKE ALL ON FUNCTION prodo_notify_approval_pending(UUID, UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION prodo_notify_approval_pending(UUID, UUID) TO prodo_app;
