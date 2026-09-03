-- S4G-06, Track S4G (forward-pull, menutup implementation_gaps.md IG-19):
-- skema penuh sudah didokumentasikan sejak awal (DATABASE_SCHEMA.md §5.21),
-- diambil persis dari sana. HANYA migration minimal -- fitur upload/lampiran
-- task sungguhan TETAP belum dibangun (di luar scope task ini, IG-19 sengaja
-- tidak di-forward-pull penuh dulu, lihat rekomendasinya). Alasan migration
-- ini dibuat SEKARANG: kolom sum(size_bytes) dibutuhkan Workspace menu
-- (S4G-05) untuk kolom STORAGE per-workspace -- tanpa tabel ini sama sekali
-- tidak ada sumber data apa pun untuk dijumlah (bukan cuma "belum terisi").
--
-- task_id/comment_id TANPA FK constraint (beda dari desain skema final) --
-- tabel tasks/task_comments belum ada sama sekali. Ditambahkan sebagai FK
-- sungguhan begitu kedua tabel itu dibuat (migration terpisah, forward-
-- looking, JANGAN lupa saat epic Task dikerjakan).
CREATE TABLE task_attachments (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  task_id        UUID,
  comment_id     UUID,
  uploader_id    UUID NOT NULL REFERENCES users(id),
  original_name  VARCHAR(512) NOT NULL,
  display_name   VARCHAR(512) NOT NULL,
  storage_key    TEXT NOT NULL,
  mime_type      VARCHAR(255) NOT NULL,
  size_bytes     BIGINT NOT NULL,
  is_image       BOOLEAN NOT NULL DEFAULT FALSE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at     TIMESTAMPTZ,
  CONSTRAINT ck_task_attachments_size CHECK (size_bytes > 0 AND size_bytes <= 52428800)
);

CREATE INDEX idx_task_attachments_task_id ON task_attachments (task_id);
CREATE INDEX idx_task_attachments_comment_id ON task_attachments (comment_id);
