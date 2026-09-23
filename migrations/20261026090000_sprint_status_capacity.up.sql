-- Sprint status 3-state + goal + audit (Track S5, menu "Sprint" PM,
-- implementation_gaps.md IG-92). Dibangun sesuai "PM Sprint.dc.html"/
-- "PM Add Sprint.dc.html" (Claude Design) yang minta 3 status berbeda
-- (BACKLOG/AKTIF/SELESAI) -- sprints.is_active BOOLEAN (keputusan sengaja
-- IG-46 demi scope minimal Phase 1) TIDAK BISA membedakan "belum pernah
-- dimulai" dari "sudah selesai" (keduanya is_active=false). Diganti total
-- (bukan kolom tambahan berdampingan) supaya tidak ada 2 sumber kebenaran
-- yang bisa tidak sinkron.
CREATE TYPE sprint_status AS ENUM ('backlog', 'active', 'done');

ALTER TABLE sprints ADD COLUMN status sprint_status NOT NULL DEFAULT 'backlog';
ALTER TABLE sprints ADD COLUMN goal TEXT;

-- Backfill: is_active=true -> active. is_active=false tidak bisa dibedakan
-- backlog/done secara pasti (informasi itu hilang sejak awal) -- heuristik:
-- end_date sudah lewat (atau tidak ada end_date sama sekali tapi sprint
-- lama) dianggap 'done', sisanya 'backlog'. Data test lokal, bukan
-- retrofit sensitif seperti IG-29/IG-86.
UPDATE sprints SET status = CASE
  WHEN is_active THEN 'active'::sprint_status
  WHEN end_date IS NOT NULL AND end_date < CURRENT_DATE THEN 'done'::sprint_status
  ELSE 'backlog'::sprint_status
END;

ALTER TABLE sprints DROP COLUMN is_active;
