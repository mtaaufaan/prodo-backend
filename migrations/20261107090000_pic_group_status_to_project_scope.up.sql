-- pic_group_configs.status_id sempat menunjuk status template WORKSPACE,
-- sebelum Track S5B (migrasi IG-101) membuat salinan status per-PROJECT dan
-- memindahkan task ke sana -- tapi migrasi itu tidak menyentuh tabel ini.
-- Baris yang masih menunjuk status workspace dipetakan ke status project
-- dengan NAMA yang sama (pola sama rule automation IG-101: cocokkan nama,
-- bukan ID); yang tidak punya padanan dihapus (Full handoff = perilaku
-- default untuk status tanpa PIC Group, bukan kehilangan akses).
-- Idempoten: kalau tidak ada baris yang menunjuk status workspace, tidak ada
-- yang berubah (di DB dev tabel ini kosong saat migrasi dibuat).
UPDATE pic_group_configs pgc
SET status_id = ps.id
FROM custom_statuses ws, custom_statuses ps
WHERE ws.id = pgc.status_id
  AND ws.scope_type = 'workspace'
  AND ps.scope_type = 'project'
  AND ps.scope_id = pgc.project_id
  AND ps.name = ws.name
  AND NOT EXISTS (
    SELECT 1 FROM pic_group_configs dup
    WHERE dup.project_id = pgc.project_id AND dup.status_id = ps.id AND dup.user_id = pgc.user_id
  );

DELETE FROM pic_group_configs pgc
USING custom_statuses cs
WHERE cs.id = pgc.status_id
  AND cs.scope_type = 'workspace';
