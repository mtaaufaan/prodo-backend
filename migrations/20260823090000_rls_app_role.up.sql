-- S2-10 prasyarat: RLS_DESIGN.md §2 mendefinisikan role terpisah
-- prodo_app (non-superuser, kena RLS) supaya app runtime TIDAK pernah
-- pakai superuser `prodo` (dari DATABASE_URL, dipakai migrate CLI/seed) --
-- superuser SELALU bypass RLS apapun policy-nya (bahkan dengan FORCE ROW
-- LEVEL SECURITY), jadi tanpa role ini seluruh S2-10 hanya teater.
--
-- `prodo_migrator` (RLS_DESIGN.md §2.1, BYPASSRLS) SENGAJA tidak dibuat
-- terpisah -- `prodo` superuser yang sudah ada sejak S0 sudah memenuhi
-- peran itu (satu-satunya pemakainya adalah migrate CLI/seed, tidak
-- pernah dipakai runtime). `prodo_readonly` juga belum dibuat -- belum
-- ada consumer (Grafana/analytics) yang butuh di S2.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'prodo_app') THEN
    CREATE ROLE prodo_app NOINHERIT NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'prodo_app_user') THEN
    -- Password dev-only, sama pola dengan prodo_dev_secret di .env.example
    -- -- rotasi OpenBao (RLS_DESIGN.md §2.1) baru relevan di prod.
    CREATE ROLE prodo_app_user LOGIN PASSWORD 'prodo_app_dev_secret' IN ROLE prodo_app;
  END IF;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO prodo_app;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO prodo_app;

-- Supaya tabel yang dibuat migration BERIKUTNYA (dijalankan sebagai
-- `prodo`, role yang mengeksekusi ALTER DEFAULT PRIVILEGES ini) otomatis
-- ke-grant ke prodo_app tanpa perlu diingat di setiap migration baru.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO prodo_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT USAGE ON SEQUENCES TO prodo_app;
