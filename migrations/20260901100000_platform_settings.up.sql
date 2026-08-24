-- S4P-18 (US-070): satu baris singleton (id=1 CHECK) untuk pengaturan
-- keamanan platform yang bisa diubah lewat FE PlatformSecuritySettings --
-- session timeout Platform Admin TIDAK LAGI env var statis (PA_SESSION_IDLE_TIMEOUT,
-- S4P-15), supaya perubahan langsung berlaku tanpa redeploy. Tidak ada RLS
-- (konsisten dengan platform_admin_ip_allowlist, IG-07: tabel platform-level
-- sengaja tidak di-RLS).
CREATE TABLE platform_settings (
  id                      SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  pa_session_idle_timeout INTERVAL NOT NULL DEFAULT '10 minutes',
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO platform_settings (id) VALUES (1);
