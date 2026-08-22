-- Prasyarat teknis S2-05 (bukan S2-05 sendiri) -- notifikasi role-changed
-- (S2-05) butuh tabel notifications, tapi migrasinya literally dijadwalkan
-- S6-29 (Sprint 6, jauh setelah S2) -- pola sama persis dengan
-- 20260822090000_org_hierarchy_prerequisite.up.sql (IG-09). Majukan migrasi
-- MINIMAL tabel ini sekarang, kolom persis sesuai DATABASE_SCHEMA.md §5.26
-- (bukan wording S6-29 asli yang juga sudah usang: "account_id" tidak ada
-- di skema final, kolom sebenarnya "user_id"). Index/fitur lain yang jadi
-- scope penuh S6 tetap dikerjakan nanti sesuai jadwal aslinya.
CREATE TABLE notifications (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actor_id     UUID REFERENCES users(id),
  type         VARCHAR(100) NOT NULL,
  entity_type  VARCHAR(50),
  entity_id    UUID,
  title        VARCHAR(512),
  body         TEXT,
  is_read      BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_unread ON notifications (user_id, is_read);
