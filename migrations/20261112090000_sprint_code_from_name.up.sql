-- Kode sprint mengikuti nomor di nama sprint (IG-120): nama yang mengandung
-- "Sprint 0" -> SPR-00, "Sprint 3 - Reader" -> SPR-03, dst. Hanya menyentuh
-- sprint yang kodenya MASIH kode otomatis (pola SPR-<angka>) -- kode kustom
-- hasil import / input manual tidak diubah.
--   Fase 1: kode kandidat dilepas ke placeholder (supaya tukar kode tidak bentrok unique index)
--   Fase 2: kode = SPR-<nomor di nama>, hanya untuk sprint PERTAMA per nomor per project
--           dan kalau kode itu belum dipegang sprint lain
--   Fase 3: sisanya mendapat angka bebas terkecil
DO $$
DECLARE
  r RECORD;
  n INT;
  target TEXT;
BEGIN
  UPDATE sprints SET code = 'TMP-' || substr(replace(id::text, '-', ''), 1, 12)
  WHERE code ~ '^SPR-[0-9]+$' AND name ~* 'sprint\s*[0-9]+';

  FOR r IN
    SELECT id, project_id,
           (regexp_match(name, 'sprint\s*([0-9]+)', 'i'))[1]::int AS num,
           row_number() OVER (PARTITION BY project_id, (regexp_match(name, 'sprint\s*([0-9]+)', 'i'))[1]::int ORDER BY created_at, id) AS dup
    FROM sprints WHERE code LIKE 'TMP-%'
    ORDER BY project_id, created_at, id
  LOOP
    target := 'SPR-' || CASE WHEN r.num < 10 THEN '0' || r.num::text ELSE r.num::text END;
    IF r.dup = 1 AND NOT EXISTS (SELECT 1 FROM sprints WHERE project_id = r.project_id AND upper(code) = target) THEN
      UPDATE sprints SET code = target WHERE id = r.id;
    END IF;
  END LOOP;

  FOR r IN SELECT id, project_id FROM sprints WHERE code LIKE 'TMP-%' ORDER BY project_id, created_at, id
  LOOP
    n := 1;
    LOOP
      target := 'SPR-' || CASE WHEN n < 10 THEN '0' || n::text ELSE n::text END;
      EXIT WHEN NOT EXISTS (SELECT 1 FROM sprints WHERE project_id = r.project_id AND upper(code) = target);
      n := n + 1;
    END LOOP;
    UPDATE sprints SET code = target WHERE id = r.id;
  END LOOP;
END $$;
