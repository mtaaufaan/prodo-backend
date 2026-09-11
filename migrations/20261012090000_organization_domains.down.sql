ALTER TABLE organizations ADD COLUMN domain VARCHAR(255);

-- Kembalikan domain PERTAMA yang tercatat per organisasi (urutan
-- created_at) -- rollback skema penuh, tapi organisasi dengan >1 domain
-- kehilangan sisanya (batasan struktural kolom tunggal yang digantikan
-- migrasi ini, sama seperti alasan migrasi ini dibuat).
UPDATE organizations o
SET domain = sub.domain
FROM (
  SELECT DISTINCT ON (organization_id) organization_id, domain
  FROM organization_domains
  ORDER BY organization_id, created_at
) sub
WHERE o.id = sub.organization_id;

ALTER TABLE organizations ADD CONSTRAINT ck_organizations_domain_format
  CHECK (domain IS NULL OR domain ~* '^[a-z0-9.-]+\.[a-z]{2,}$');

DROP TABLE organization_domains;
