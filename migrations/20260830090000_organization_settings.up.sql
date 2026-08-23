-- S3-29/32 (US-010/US-011): default_language + storage quota (byte-precision).
-- default_language: ENUM sesuai wording task -- organizations belum punya
-- kolom bahasa sama sekali (beda dari users.locale CHAR(2), §5.1, yang
-- sudah ada sejak S1 untuk preferensi PER-USER, bukan default organisasi).
CREATE TYPE org_language AS ENUM ('id', 'en');

ALTER TABLE organizations
  ADD COLUMN default_language org_language NOT NULL DEFAULT 'id';

-- storage_quota_bytes/storage_max_bytes -- KOLOM BARU byte-precision,
-- MELENGKAPI (bukan menggantikan) storage_quota_mb/storage_used_mb yang
-- sudah ada sejak S2-01 forward-pull (dipakai S3-06 Summary). Dua sistem
-- ini disengaja terpisah: storage_used_mb tetap sumber kebenaran usage
-- (S3-33 real-time calc dari `attachments` BELUM bisa dibangun -- tabel
-- itu belum ada, lihat implementation_gaps.md IG-19), sementara
-- storage_quota_bytes/storage_max_bytes murni untuk fitur BARU S3-34 (GA
-- set kuota, divalidasi terhadap batas dari Platform Admin, glossary.md
-- "Storage Quota"). Default storage_max_bytes 100 GB (batas generous,
-- belum ada mekanisme Platform Admin mengubahnya per-grup/tier --
-- itu scope Tier Layanan, S12+); storage_quota_bytes default 10 GB, sama
-- dengan default storage_quota_mb (10240 MB) yang sudah ada supaya
-- konsisten untuk organisasi existing.
ALTER TABLE organizations
  ADD COLUMN storage_quota_bytes BIGINT NOT NULL DEFAULT 10737418240,
  ADD COLUMN storage_max_bytes BIGINT NOT NULL DEFAULT 107374182400,
  ADD CONSTRAINT ck_org_storage_quota_within_max CHECK (storage_quota_bytes <= storage_max_bytes);
