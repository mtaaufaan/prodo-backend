-- Hanya hapus CANCELED sistem yang belum dipakai task manapun (FK tasks.status_id).
DELETE FROM custom_statuses cs
WHERE cs.is_system AND cs.name = 'CANCELED'
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.status_id = cs.id);
ALTER TABLE custom_statuses DROP COLUMN IF EXISTS require_pic;
