-- S4P-20, US-071: jejak audit level platform, TERPISAH dari audit_logs
-- (yang mencakup aksi org/workspace member) supaya Group Admin tidak
-- pernah bisa mengaksesnya (RLS) -- skema kolom sama persis dengan
-- audit_logs (docs/DATABASE_SCHEMA.md §5.27), lihat migrations/
-- 20260820150100_platform_invitations_audit_logs.up.sql.
CREATE TABLE platform_audit_logs (
  id           UUID DEFAULT uuid_generate_v4(),
  org_id       UUID,
  workspace_id UUID,
  actor_id     UUID,
  actor_role   TEXT,
  actor_ip     INET,
  action       VARCHAR(100) NOT NULL,
  entity_type  VARCHAR(50) NOT NULL,
  entity_id    UUID,
  state_before JSONB,
  state_after  JSONB,
  metadata     JSONB,
  logged_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (logged_at);

CREATE TABLE platform_audit_logs_2025 PARTITION OF platform_audit_logs
  FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
CREATE TABLE platform_audit_logs_2026 PARTITION OF platform_audit_logs
  FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
CREATE TABLE platform_audit_logs_2027 PARTITION OF platform_audit_logs
  FOR VALUES FROM ('2027-01-01') TO ('2028-01-01');

CREATE INDEX idx_platform_audit_logs_actor ON platform_audit_logs (actor_id);
CREATE INDEX idx_platform_audit_logs_entity ON platform_audit_logs (entity_type, entity_id);
CREATE INDEX idx_platform_audit_logs_date ON platform_audit_logs (logged_at DESC);
CREATE INDEX idx_platform_audit_logs_action ON platform_audit_logs (action);

-- RLS: SELECT cuma untuk Platform Admin (US-071 AC). INSERT sengaja TIDAK
-- dibatasi prodo_is_platform_admin() -- semua pemanggil insert (lihat
-- account_repository.go logAudit/logTierAudit dan sejenisnya) sudah
-- digerbangi RequirePlatformAdmin() di layer HTTP, dan mewajibkan RLS
-- context di setiap transaksi insert akan menambah ~10 titik perubahan
-- tanpa manfaat keamanan tambahan (lihat implementation_gaps.md IG-24
-- untuk kasus serupa yang butuh RLS context saat READ).
ALTER TABLE platform_audit_logs ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform_audit_logs_select ON platform_audit_logs
  FOR SELECT USING (prodo_is_platform_admin());
CREATE POLICY platform_audit_logs_insert ON platform_audit_logs
  FOR INSERT WITH CHECK (true);
