-- Pre-fill Nama/Jabatan undangan Eksekutif SEBELUM aktivasi (permintaan
-- user 2026-09-10 -- tidak realistis meminta Direksi mengisi data ini
-- sendiri sebelum akun aktif, jadi GA mengisikannya lewat "Kelola" di
-- Members & Roles sementara undangan masih pending). Nullable + tanpa
-- constraint executive-only: kolom ini bermakna hanya untuk baris
-- is_executive_invite = TRUE, tapi tidak perlu CHECK tambahan (baris
-- workspace biasa cukup tidak pernah menulisinya). Tipe sama dengan
-- users.display_name/title (VARCHAR(255)/TEXT) supaya nilainya bisa
-- dipindah apa adanya saat AcceptExecutiveInvitation.
ALTER TABLE user_invitations
  ADD COLUMN display_name VARCHAR(255),
  ADD COLUMN title TEXT;
