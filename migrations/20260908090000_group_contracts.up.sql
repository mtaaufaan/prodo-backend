-- Dikonfirmasi user 2026-08-29: kontrak adalah hubungan komersial
-- Platform Admin <-> Group Admin (langganan grup), BUKAN properti
-- organisasi individual -- lihat juga migrasi 20260906090000 (bukan
-- terkait langsung, sesi diskusi yang sama). Disimpan di tabel
-- TERPISAH (bukan kolom di groups) supaya riwayat perpanjangan
-- otomatis terjaga -- "kontrak aktif" grup = baris dengan end_at
-- paling baru per group_id, TIDAK ada kolom flag is_current terpisah
-- (hindari dua sumber kebenaran yang bisa tidak sinkron).
--
-- organizations.retention_days TETAP per-organisasi (bisa beda antar
-- org dalam satu grup, tidak diubah oleh migrasi ini) -- purge data
-- organisasi secara konsep = kontrak_aktif_grup.end_at +
-- organizations.retention_days (purge_scheduled_at sendiri belum
-- dipakai kode manapun, murni dokumentasi skema untuk saat ini).
CREATE TABLE group_contracts (
  id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  start_at            TIMESTAMPTZ NOT NULL,
  subscription_period VARCHAR(20) NOT NULL,
  end_at              TIMESTAMPTZ NOT NULL,
  invoice_number      VARCHAR(100),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_by          UUID REFERENCES users(id),
  CONSTRAINT chk_group_contracts_period CHECK (subscription_period IN ('monthly', 'quarterly', 'yearly')),
  CONSTRAINT chk_group_contracts_dates CHECK (end_at > start_at)
);

CREATE INDEX idx_group_contracts_group_id_end_at ON group_contracts (group_id, end_at DESC);

-- Backfill: satu kontrak awal per grup yang sudah punya organisasi
-- berkontrak, tanggal mulai diperkirakan mundur dari end_at existing
-- (asumsi bulanan -- data dev/uji, bukan produksi, jadi diperkirakan
-- cukup). Ambil yang PALING AWAL per grup sebagai default konservatif.
INSERT INTO group_contracts (group_id, start_at, subscription_period, end_at, created_at)
SELECT g.id, earliest.contract_end_at - INTERVAL '1 month', 'monthly', earliest.contract_end_at, NOW()
FROM groups g
JOIN LATERAL (
  SELECT MIN(o.contract_end_at) AS contract_end_at FROM organizations o
  WHERE o.group_id = g.id AND o.contract_end_at IS NOT NULL
) earliest ON earliest.contract_end_at IS NOT NULL;

ALTER TABLE organizations DROP COLUMN contract_end_at;
