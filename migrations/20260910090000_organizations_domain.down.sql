ALTER TABLE organizations DROP CONSTRAINT IF EXISTS ck_organizations_domain_format;
ALTER TABLE organizations DROP COLUMN domain;
