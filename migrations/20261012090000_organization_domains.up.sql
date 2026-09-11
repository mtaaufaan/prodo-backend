-- organization_domains: organizations.domain (kolom tunggal, v1.9.0 S4G-02)
-- menjadi tabel one-to-many -- satu organisasi bisa punya lebih dari satu
-- domain email resmi (dikonfirmasi user 2026-09-11, sebelumnya cuma 1
-- domain per organisasi). Constraint format PERSIS sama dengan
-- ck_organizations_domain_format lama, dipindah ke sini per-baris.
--
-- UNIK PER-ORGANISASI saja (BUKAN unik global lintas seluruh organisasi) --
-- dikonfirmasi user: organisasi lain BOLEH pakai domain yang sama, bukan
-- indikasi kesalahan/kolusi (lihat implementation_gaps.md untuk keputusan
-- ini beserta trade-off-nya).
CREATE TABLE organization_domains (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  domain          VARCHAR(255) NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_organization_domains_org_domain UNIQUE (organization_id, domain),
  CONSTRAINT ck_organization_domains_format CHECK (domain ~* '^[a-z0-9.-]+\.[a-z]{2,}$')
);

CREATE INDEX idx_organization_domains_org_id ON organization_domains (organization_id);

-- Migrasi data lama: domain tunggal (kalau ada) jadi baris pertama supaya
-- tidak ada organisasi yang tiba-tiba kehilangan domain resminya.
INSERT INTO organization_domains (organization_id, domain)
SELECT id, domain FROM organizations WHERE domain IS NOT NULL;

ALTER TABLE organizations DROP CONSTRAINT ck_organizations_domain_format;
ALTER TABLE organizations DROP COLUMN domain;

ALTER TABLE organization_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization_domains FORCE ROW LEVEL SECURITY;

-- prodo_is_group_admin_of_org sudah ada sejak migrasi
-- 20260827090000_rls_organizations_workspaces -- reuse penuh, sama pola
-- policy `workspaces` (join ke organizations, TABEL LAIN yang sudah
-- committed/visible, aman dari masalah command-counter visibility yang
-- didokumentasikan di migrasi itu).
CREATE POLICY organization_domains_select ON organization_domains
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_org(organization_id));

CREATE POLICY organization_domains_insert ON organization_domains
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_org(organization_id));

CREATE POLICY organization_domains_delete ON organization_domains
  FOR DELETE TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_org(organization_id));
