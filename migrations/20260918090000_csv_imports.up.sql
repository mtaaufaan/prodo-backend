-- Import Data (S4G-15/16/17/18, Track S4G, desain "GA Import Data.dc.html").
-- Kolom project_id di §5.36 DATABASE_SCHEMA.md (rancangan lama, tidak
-- pernah diterapkan) diganti org_id+group_id -- import di sini SELALU
-- level organisasi (member & role), bukan level project. kind dibatasi
-- 'member' saja untuk sekarang -- jenis "task" dari desain tidak bisa
-- dibangun, tabel tasks belum ada (Task Management Core belum dibangun,
-- lihat implementation_gaps.md).
CREATE TABLE csv_imports (
  id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  group_id           UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind               VARCHAR(10) NOT NULL DEFAULT 'member',
  imported_by        UUID NOT NULL REFERENCES users(id),
  actor_role         VARCHAR(30) NOT NULL,
  original_filename  VARCHAR(512) NOT NULL,
  storage_key        TEXT NOT NULL,
  status             VARCHAR(12) NOT NULL DEFAULT 'pending',
  total_rows         INTEGER,
  success_count      INTEGER,
  failed_count        INTEGER,
  row_results        JSONB,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at       TIMESTAMPTZ
);

ALTER TABLE csv_imports ADD CONSTRAINT chk_csv_imports_kind CHECK (kind IN ('member'));
ALTER TABLE csv_imports ADD CONSTRAINT chk_csv_imports_status CHECK (status IN ('pending', 'running', 'completed', 'failed'));

CREATE INDEX idx_csv_imports_group_id ON csv_imports (group_id);

ALTER TABLE csv_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE csv_imports FORCE ROW LEVEL SECURITY;

CREATE POLICY csv_imports_select ON csv_imports
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

CREATE POLICY csv_imports_insert ON csv_imports
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR prodo_is_group_admin_of_group(group_id));

-- UPDATE (status/hasil) ditulis job Asynq lewat konteks bypass
-- platform_admin (trusted background process, pola sama
-- StorageQuotaCheckJob/RetentionNotifyJob) -- tidak ada jalur GA langsung
-- UPDATE baris ini dari HTTP request.
CREATE POLICY csv_imports_update ON csv_imports
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin());
