-- S3-04 (US-007): kolom deactivated_at belum ada di DATABASE_SCHEMA.md §5.7 --
-- wording asli task menyebut kolom "status", tapi skema final tidak punya
-- kolom status generik. Pola sama dengan workspaces.archived_at (§5.9):
-- NULL = aktif, diisi = dinonaktifkan. Bukan penghapusan data (US-007 AC:
-- "seluruh akses member diblokir sementara data tetap tersimpan").
ALTER TABLE organizations ADD COLUMN deactivated_at TIMESTAMPTZ NULL DEFAULT NULL;
