-- Dikonfirmasi user 2026-08-29: membalik scope S4P-18 -- session timeout
-- Platform Admin jadi PER-AKUN (bukan lagi satu setting global untuk
-- semua PA), IP allowlist jadi GLOBAL untuk seluruh akun platform_admin
-- (bukan lagi per-akun), dengan flag eksplisit ip_allowlist_enabled
-- supaya PA bisa menonaktifkan enforcement sementara tanpa menghapus
-- daftar CIDR yang sudah dikonfigurasi.

-- Override per-akun; NULL berarti PA belum pernah mengatur sendiri, pakai
-- fallback platform_settings.pa_session_idle_timeout (kolom lama
-- dipertahankan sebagai default sistem, TIDAK dihapus).
ALTER TABLE users ADD COLUMN pa_session_idle_timeout_seconds INTEGER;

ALTER TABLE platform_settings ADD COLUMN ip_allowlist_enabled BOOLEAN NOT NULL DEFAULT false;
