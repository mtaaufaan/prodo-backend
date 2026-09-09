-- S4G-27 (US-010, Bahasa & Format Regional lanjutan) -- format tanggal/
-- waktu/zona waktu/angka LEVEL GRUP, di luar organizations.default_language
-- (S3-29-31, per-organisasi, sudah ada). Belum ada kolom locale level grup
-- sama sekali di skema manapun sebelum ini -- desain "GA Bahasa Lokal.dc.html"
-- membatasi tiap field ke set pilihan tetap (dropdown), jadi dipakai ENUM
-- (pola sama org_language) bukan VARCHAR+CHECK bebas.
CREATE TYPE group_date_format AS ENUM ('DD/MM/YYYY', 'YYYY-MM-DD', 'DD MMM YYYY');
CREATE TYPE group_time_format AS ENUM ('24h', '12h');
CREATE TYPE group_timezone AS ENUM ('Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura', 'UTC');
-- number_format: nilai locale ICU/Intl (dipakai langsung oleh
-- Intl.NumberFormat di FE), bukan string contoh literal dari desain
-- ("1.234,56"/"1,234.56") -- lebih tahan lama dan langsung dipakai ulang
-- untuk pratinjau tanpa parsing token.
CREATE TYPE group_number_format AS ENUM ('id-ID', 'en-US');

ALTER TABLE groups
  ADD COLUMN locale_date_format group_date_format NOT NULL DEFAULT 'DD/MM/YYYY',
  ADD COLUMN locale_time_format group_time_format NOT NULL DEFAULT '24h',
  ADD COLUMN locale_timezone group_timezone NOT NULL DEFAULT 'Asia/Jakarta',
  ADD COLUMN locale_number_format group_number_format NOT NULL DEFAULT 'id-ID';
