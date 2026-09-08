-- Members & Roles lanjutan (atas permintaan user): "Jabatan" (title) berlaku
-- untuk SEMUA user, bukan cuma Eksekutif -- konsolidasi dari
-- executive_assignments.title (migrasi 20260915090200) yang sekarang jadi
-- redundan. Ditaruh di users (bukan per-assignment) karena title adalah
-- atribut orangnya, bukan penugasan tertentu -- dipakai juga untuk daftar
-- Admin Workspace per workspace (Nama/Email/Jabatan/Status).
ALTER TABLE users ADD COLUMN title TEXT;

-- Pindahkan data yang mungkin sudah terisi (kalau ada) sebelum kolom lama
-- dihapus di migrasi berikut.
UPDATE users u
SET title = ea.title
FROM executive_assignments ea
WHERE ea.user_id = u.id AND ea.title IS NOT NULL AND u.title IS NULL;
