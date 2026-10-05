-- 1) Parameter per status "wajib menetapkan PIC saat task masuk status ini"
--    (US-017 sebelumnya mewajibkan PIC di SETIAP perpindahan status). Default
--    TRUE = perilaku lama; status akhir DONE/CANCELED dimatikan di langkah 3.
ALTER TABLE custom_statuses ADD COLUMN require_pic BOOLEAN NOT NULL DEFAULT TRUE;

-- 2) Status sistem baru CANCELED (BLOCKED tetap dipertahankan) untuk SETIAP
--    scope (template workspace + salinan per-project). Scope yang sudah punya
--    status bernama CANCELED dilewati (nama tidak unik di level DB). Posisi =
--    di ujung kanan scope tersebut. Batas 12 status tidak ditegakkan di sini.
INSERT INTO custom_statuses (scope_type, scope_id, name, color_token, position, is_system, require_pic)
SELECT scope_type, scope_id, 'CANCELED', 'grey', MAX(position) + 1, TRUE, FALSE
FROM custom_statuses
GROUP BY scope_type, scope_id
HAVING NOT bool_or(upper(name) = 'CANCELED');

-- 3) Default: status akhir tidak butuh PIC.
UPDATE custom_statuses SET require_pic = FALSE WHERE is_system AND name IN ('DONE', 'CANCELED');
