-- US-012 susulan (dikonfirmasi user 2026-10-18): status siklus hidup project
-- dan tanggal berakhir -- disebut di acceptance criteria backlog.md sejak
-- awal ("tanggal mulai, tanggal selesai, dan status awal") tapi tidak pernah
-- masuk ke desain final ("AW Add Project.dc.html"/"AW Projects.dc.html")
-- ataupun skema §5.12 -- gap yang baru sekarang ditutup atas permintaan
-- eksplisit. Terpisah dari is_archived (§5.12) -- arsip murni soal akses
-- baca-saja, status ini soal progres pekerjaan. "tanggal mulai" TIDAK
-- diminta user, sengaja tidak ditambahkan.
CREATE TYPE project_lifecycle_status AS ENUM ('not_started', 'in_progress', 'completed', 'on_hold');

ALTER TABLE projects
  ADD COLUMN status project_lifecycle_status NOT NULL DEFAULT 'not_started',
  ADD COLUMN end_date DATE;
