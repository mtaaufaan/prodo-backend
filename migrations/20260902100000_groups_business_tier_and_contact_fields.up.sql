-- Koreksi wording (S4 H4 lanjutan, audit vs desain "PA Group Admin
-- Form.dc.html"/pa-store.js): groups.tier sebelumnya mengizinkan
-- 'professional' (S2-01, sebelum desain detail ini ada), tapi sumber
-- desain yang lebih detail dan belakangan memakai 'business'. Tidak ada
-- baris groups yang memakai 'professional' -- aman diganti langsung.
UPDATE groups SET tier = 'business' WHERE tier = 'professional';
ALTER TABLE groups DROP CONSTRAINT chk_groups_tier;
ALTER TABLE groups ADD CONSTRAINT chk_groups_tier CHECK (tier IN ('starter', 'business', 'enterprise'));

-- Field kontak perusahaan (S4P-06, desain "Tambah Group Admin"/"PA Group
-- Admin Form") -- disimpan di groups (properti perusahaan/grup, konsisten
-- dengan field "Nama Perusahaan / Grup" = groups.name yang sudah ada),
-- bukan di users individual.
ALTER TABLE groups ADD COLUMN job_title VARCHAR(255);
ALTER TABLE groups ADD COLUMN address TEXT;
ALTER TABLE groups ADD COLUMN phone VARCHAR(50);

-- storage_quota_gb NULL berarti pakai plafon default tier
-- (service_tiers.max_storage_gb) -- lihat AccountRepository.plafonOf
-- setara di Go. BELUM ditegakkan sebagai ceiling lintas-organisasi
-- (beda dari organizations.storage_max_bytes per-org yang sudah ada
-- sejak S3-34) -- baru disimpan dan ditampilkan di form PA, penegakan
-- agregat menyusul kalau dibutuhkan (di luar scope task ini, dicatat di
-- sprint_backlog.md).
ALTER TABLE groups ADD COLUMN storage_quota_gb INTEGER;
