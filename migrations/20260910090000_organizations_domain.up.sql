-- S4G-02 (US-007, Track S4G): kolom `domain` (domain email resmi organisasi)
-- dari desain asli "GA Organizations.dc.html" -- disebut di wording S3-03
-- ("nama/logo/domain") tapi SENGAJA ditunda saat itu karena skema §5.7
-- belum punya kolomnya (dikonfirmasi user 2026-08-26, S3 H2). Ditambahkan
-- sekarang karena "GA Organizations" jadi bagian aktif Track S4G. `logo`
-- TETAP ditunda -- desain cuma tampilkan placeholder upload, belum ada
-- storage/CDN untuk file organisasi (di luar scope Track S4G).
ALTER TABLE organizations ADD COLUMN domain VARCHAR(255);

ALTER TABLE organizations
  ADD CONSTRAINT ck_organizations_domain_format CHECK (domain IS NULL OR domain ~* '^[a-z0-9.-]+\.[a-z]{2,}$');
