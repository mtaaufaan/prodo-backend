-- S4G Data Retention (dikonfirmasi user 2026-09-08): workspace butuh
-- soft-delete sama pola projects (§5.12) supaya bisa masuk Jadwal
-- Penghapusan dan dipulihkan -- sebelumnya WorkspaceRepository.Delete
-- HARD DELETE permanen tanpa jejak, satu-satunya entitas tanpa jalur
-- retensi sama sekali.
ALTER TABLE workspaces ADD COLUMN deleted_at TIMESTAMPTZ NULL DEFAULT NULL;
ALTER TABLE workspaces ADD COLUMN purge_scheduled_at TIMESTAMPTZ NULL DEFAULT NULL;
