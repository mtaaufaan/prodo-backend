-- H20-22 (S4W-19/20/21, EPIC 10 Attachment Management). Menambah
-- `purge_scheduled_at` ke `task_attachments` -- pola PERSIS
-- workspaces/projects/organizations (deleted_at + purge_scheduled_at =
-- deleted_at + retention_days organisasi pemilik). Dua mode hapus di
-- desain "AW Documents.dc.html": "retensi" (default, restorable selama
-- purge_scheduled_at belum lewat) vs "permanen" (AW-only) yang menyetel
-- purge_scheduled_at = deleted_at (jendela pulih nol, dan objek MinIO
-- langsung dihapus fisik oleh service, TIDAK menunggu job purge).
--
-- TIDAK ada job Asynq yang benar-benar mengeksekusi purge fisik begitu
-- purge_scheduled_at lewat -- konsisten gap yang SUDAH diterima di
-- seluruh tabel purge_scheduled_at lain (organizations/workspaces/
-- projects, lihat implementation_gaps.md IG-60 dkk: kolom dihitung dan
-- ditampilkan di jadwal retensi, tapi purge fisik otomatis belum pernah
-- dibangun di mana pun). Bukan gap baru yang diperkenalkan di sini.
ALTER TABLE task_attachments ADD COLUMN purge_scheduled_at TIMESTAMPTZ;

CREATE INDEX idx_task_attachments_purge ON task_attachments (purge_scheduled_at)
  WHERE purge_scheduled_at IS NOT NULL;
