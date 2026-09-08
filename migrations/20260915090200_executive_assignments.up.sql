-- S16-07 (forward-pull, Track S4G Members & Roles): DDL persis
-- DATABASE_SCHEMA.md §5.38, dijalankan SETELAH 20260915090000 (enum
-- 'executive' sudah commit di transaksi terpisah -- aman dipakai di sini).
--
-- Kolom `title` (Jabatan) TIDAK ADA di §5.38 tertulis -- gap-fill untuk
-- panel "Identitas Eksekutif" di desain "GA Members Roles.dc.html" yang
-- butuh field Nama+Jabatan yang bisa diedit GA. Ditaruh di sini (bukan
-- users.title) karena sifatnya melekat ke PENUGASAN eksekutif per grup,
-- bukan identitas user secara umum -- dicatat implementation_gaps.md.
--
-- Wewenang assignment: DATABASE_SCHEMA.md §5.38 tertulis "Assignment
-- dilakukan oleh Platform Admin, GA cuma mengusulkan" -- SUDAH USANG,
-- bertentangan dengan AC resmi US-086 (docs/backlog.md baris 181): "Role
-- Eksekutif ditugaskan oleh Group Admin -- Platform Admin tidak terlibat".
-- DATABASE_SCHEMA.md diperbaiki di commit yang sama dengan migrasi ini.
CREATE TABLE executive_assignments (
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  title       TEXT,
  assigned_by UUID REFERENCES users(id),
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, group_id)
);

CREATE INDEX idx_executive_assignments_group ON executive_assignments (group_id);
