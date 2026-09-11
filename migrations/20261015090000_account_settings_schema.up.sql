-- GA Pengaturan Akun (Track S4G, desain "GA Pengaturan Akun.dc.html").
--
-- users.phone: nomor telepon PRIBADI pemilik akun (tab Profil). BEDA dari
-- group_admin_assignments.phone (kontak PIC organisasi/grup, S4P-06) --
-- itu data organisasi, ini personal per users.id, sama pola konsolidasi
-- seperti users.title (IG-42).
ALTER TABLE users ADD COLUMN phone VARCHAR(50);

-- notification_preferences (docs/DATABASE_SCHEMA.md §5.35) -- sudah
-- didokumentasikan sejak v1.2.0 (dasar PATCH /users/me/notification-
-- preferences di API_CONTRACT.md §11) tapi belum pernah benar-benar
-- dimigrasikan sampai sekarang (tab Notifikasi Pengaturan Akun). Users-
-- scoped (bukan tenant-scoped), tidak di-RLS -- sama kelas dengan
-- user_sessions/user_mfa_configs.
CREATE TABLE notification_preferences (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  event_type  VARCHAR(100) NOT NULL,
  in_app      BOOLEAN NOT NULL DEFAULT TRUE,
  push        BOOLEAN NOT NULL DEFAULT TRUE,
  email       BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_notif_pref UNIQUE (user_id, event_type)
);
