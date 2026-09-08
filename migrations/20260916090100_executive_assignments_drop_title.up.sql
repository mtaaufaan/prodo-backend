-- Konsolidasi title ke users (migrasi 20260916090000) -- kolom ini jadi
-- redundan, datanya sudah dipindah di migrasi sebelumnya.
ALTER TABLE executive_assignments DROP COLUMN IF EXISTS title;
