ALTER TABLE groups ADD COLUMN tier VARCHAR(20);
UPDATE groups g SET tier = st.name FROM service_tiers st WHERE st.id = g.tier_id;
ALTER TABLE groups ALTER COLUMN tier SET NOT NULL;
ALTER TABLE groups ALTER COLUMN tier SET DEFAULT 'starter';
ALTER TABLE groups ADD CONSTRAINT chk_groups_tier CHECK (tier IN ('starter', 'business', 'enterprise'));
ALTER TABLE groups DROP COLUMN tier_id;

ALTER TABLE service_tiers DROP COLUMN archived_at;
ALTER TABLE service_tiers DROP COLUMN deactivated_at;
ALTER TABLE service_tiers DROP COLUMN is_custom;
ALTER TABLE service_tiers ADD CONSTRAINT ck_service_tiers_name CHECK (name IN ('starter', 'business', 'enterprise'));
ALTER TABLE service_tiers DROP CONSTRAINT uq_service_tiers_name;
ALTER TABLE service_tiers DROP CONSTRAINT service_tiers_pkey;
ALTER TABLE service_tiers ADD CONSTRAINT service_tiers_pkey PRIMARY KEY (name);
ALTER TABLE service_tiers DROP COLUMN id;
