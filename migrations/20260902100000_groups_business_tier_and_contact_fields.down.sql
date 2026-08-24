ALTER TABLE groups DROP COLUMN storage_quota_gb;
ALTER TABLE groups DROP COLUMN phone;
ALTER TABLE groups DROP COLUMN address;
ALTER TABLE groups DROP COLUMN job_title;

ALTER TABLE groups DROP CONSTRAINT chk_groups_tier;
ALTER TABLE groups ADD CONSTRAINT chk_groups_tier CHECK (tier IN ('starter', 'professional', 'enterprise'));
UPDATE groups SET tier = 'professional' WHERE tier = 'business';
