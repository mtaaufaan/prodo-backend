-- Track S5B (US-019, Custom Status level PROJECT untuk PM, "PM Custom
-- Status.dc.html") -- dikonfirmasi user: setiap project di-backfill jadi
-- salinan independen dari template workspace SAAT INI (bukan cuma project
-- baru ke depannya). Sebelum migrasi ini, custom_statuses cuma pernah
-- punya baris scope_type='workspace' (lihat komentar package
-- custom_status_repository.go) -- kolom scope_type/scope_id sendiri sudah
-- disiapkan sejak awal (migrasi 20260924090000) tepat untuk kasus ini.
--
-- 1) Clone: setiap project dapat salinan status workspace-nya SAAT INI
-- (key/warna/urutan/toggle konfirmasi-mulai ikut disalin, is_undefined
-- TIDAK -- status yang sudah di-UNDEFINE di workspace tidak perlu disalin
-- sebagai UNDEFINED juga, project baru mulai dari status aktif saja).
-- created_by workspace dipertahankan (bukan NULL) supaya insertCustomStatusAudit
-- tetap punya actor yang valid kalau baris ini nanti diaudit ulang.
INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system, require_start_confirmation, created_by)
SELECT 'project', p.id, ws.name, ws.color_token, ws.position, ws.is_system, ws.require_start_confirmation, ws.created_by
FROM projects p
JOIN custom_statuses ws ON ws.scope_type = 'workspace' AND ws.scope_id = p.workspace_id AND ws.is_undefined = FALSE
WHERE NOT EXISTS (
  SELECT 1 FROM custom_statuses cs WHERE cs.scope_type = 'project' AND cs.scope_id = p.id
);

-- 2) Repoint tasks.status_id: task yang sebelumnya menunjuk status
-- workspace kini menunjuk baris kloningan PROJECT-nya sendiri (dicocokkan
-- by name, satu-satunya identitas stabil lintas scope). ProjectService.Create
-- melakukan clone yang sama untuk project BARU ke depannya (lihat
-- project.go) -- migrasi ini cuma menutup gap utang untuk project existing.
UPDATE tasks t
SET status_id = cs_new.id
FROM custom_statuses cs_old, custom_statuses cs_new
WHERE t.status_id = cs_old.id
  AND cs_old.scope_type = 'workspace'
  AND cs_new.scope_type = 'project'
  AND cs_new.scope_id = t.project_id
  AND cs_new.name = cs_old.name;
