ALTER TABLE organizations
  DROP CONSTRAINT IF EXISTS ck_org_storage_quota_within_max,
  DROP COLUMN IF EXISTS storage_max_bytes,
  DROP COLUMN IF EXISTS storage_quota_bytes,
  DROP COLUMN IF EXISTS default_language;

DROP TYPE IF EXISTS org_language;
