-- S4P-02 (US-067): kolom terpisah dari is_active supaya "belum pernah
-- aktif" (invited, menunggu onboarding) dan "disuspend" (pernah aktif,
-- sengaja dinonaktifkan Platform Admin) bisa dibedakan pesan errornya, dan
-- reaktivasi tidak memaksa Group Admin mengulang alur invite+aktivasi.
ALTER TABLE users ADD COLUMN suspended_at TIMESTAMPTZ NULL;
