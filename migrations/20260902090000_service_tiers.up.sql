-- S4P-07 (forward-pull dari Hari 5 -- dibutuhkan form "Tambah/Ubah Group
-- Admin" lengkap sesuai desain PA Group Admin Form.dc.html, S4 H4
-- lanjutan). Katalog tier layanan. Nilai numerik mengikuti data demo
-- desain (pa-store.js DEFAULT_TIERS) -- ENTERPRISE dikonfirmasi via
-- screenshot user (retensi 30-365 hari, webhook 100 event/mnt, SSO
-- aktif, 10 org, 150 GB, 10.000 member); STARTER/BUSINESS mengikuti
-- angka yang sama persis dari pa-store.js (bukan dikarang).
CREATE TABLE service_tiers (
  name               VARCHAR(20) PRIMARY KEY,
  min_retention_days INTEGER NOT NULL,
  max_retention_days INTEGER NOT NULL,
  webhook_rate       INTEGER NOT NULL,
  sso_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
  max_org            INTEGER NOT NULL,
  max_storage_gb     INTEGER NOT NULL,
  max_members        INTEGER NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ck_service_tiers_name CHECK (name IN ('starter', 'business', 'enterprise'))
);

INSERT INTO service_tiers (name, min_retention_days, max_retention_days, webhook_rate, sso_enabled, max_org, max_storage_gb, max_members) VALUES
  ('starter', 30, 90, 20, FALSE, 1, 20, 250),
  ('business', 30, 180, 60, TRUE, 5, 80, 2500),
  ('enterprise', 30, 365, 100, TRUE, 10, 150, 10000);
