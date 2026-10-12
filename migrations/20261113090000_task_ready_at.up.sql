-- Kelengkapan task (US-017c) mendapat jejak waktu (IG-126): metrik "Kesiapan Task"
-- Member Performance menilai PIC backlog dari task terbentuk sampai dinyatakan
-- lengkap. Sebelumnya "lengkap" hanya flag (`completeness`) dengan waktu di
-- audit_logs saja -- audit_logs hanya terbaca anggota workspace (PM yang hanya
-- anggota project tidak bisa), jadi waktunya dijadikan data tasks sendiri
-- (otomatis ikut RLS tasks).
--   ready_at                   = kali TERAKHIR task dinyatakan lengkap; NULL kalau belum / dibuka lagi
--   completeness_reopen_count  = berapa kali task dikembalikan dari lengkap ke belum lengkap
ALTER TABLE tasks
    ADD COLUMN ready_at TIMESTAMPTZ,
    ADD COLUMN completeness_reopen_count INT NOT NULL DEFAULT 0;

-- Backfill dari audit_logs (task yang dibuat/diubah lewat aplikasi). Task hasil
-- import / data lama tanpa catatan audit tetap NULL (tidak ikut metrik).
UPDATE tasks t
SET ready_at = e.at
FROM (
    SELECT entity_id, MAX(logged_at) AS at
    FROM audit_logs
    WHERE action = 'task.completeness_changed' AND state_after ->> 'completeness' = 'complete'
    GROUP BY entity_id
) e
WHERE e.entity_id = t.id AND t.completeness = 'complete';

UPDATE tasks t
SET completeness_reopen_count = e.n
FROM (
    SELECT entity_id, COUNT(*)::INT AS n
    FROM audit_logs
    WHERE action = 'task.completeness_changed' AND state_after ->> 'completeness' = 'incomplete'
    GROUP BY entity_id
) e
WHERE e.entity_id = t.id;
