-- S4P-11 (diperluas, dikonfirmasi user): tier layanan sebelumnya dikunci ke
-- 3 nilai tetap via ck_service_tiers_name, dan groups.tier menyimpan NAMA
-- (dicocokkan manual ke service_tiers.name lewat JOIN, bukan FK). Ini
-- membuat rename tier berbahaya -- groups yang masih mereferensikan nama
-- lama langsung "yatim" (JOIN gagal cocok, semua batas tier-nya diam-diam
-- jadi 0). Migrasi ini: (1) name berhenti jadi PK, diganti id UUID supaya
-- rename aman; (2) tambah is_custom (tier standar starter/business/
-- enterprise TIDAK BISA dihapus, cuma tier custom yang PA tambahkan
-- sendiri); (3) tambah deactivated_at/archived_at (2 state independen,
-- keduanya reversible -- lihat implementation_gaps.md/sprint_backlog.md
-- untuk diskusi keputusannya); (4) groups.tier (string) diganti
-- groups.tier_id (FK sungguhan ke service_tiers.id).
ALTER TABLE service_tiers ADD COLUMN id UUID NOT NULL DEFAULT uuid_generate_v4();
ALTER TABLE service_tiers DROP CONSTRAINT service_tiers_pkey;
ALTER TABLE service_tiers ADD CONSTRAINT service_tiers_pkey PRIMARY KEY (id);
ALTER TABLE service_tiers ADD CONSTRAINT uq_service_tiers_name UNIQUE (name);
ALTER TABLE service_tiers DROP CONSTRAINT ck_service_tiers_name;
ALTER TABLE service_tiers ADD COLUMN is_custom BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE service_tiers ADD COLUMN deactivated_at TIMESTAMPTZ;
ALTER TABLE service_tiers ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE groups ADD COLUMN tier_id UUID REFERENCES service_tiers(id);
UPDATE groups g SET tier_id = st.id FROM service_tiers st WHERE st.name = g.tier;
ALTER TABLE groups ALTER COLUMN tier_id SET NOT NULL;
ALTER TABLE groups DROP CONSTRAINT chk_groups_tier;
ALTER TABLE groups DROP COLUMN tier;
