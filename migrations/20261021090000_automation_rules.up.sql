-- S4W-09 (H11), EPIC 7 Automation Engine (US-048/049/050/051/052/053) --
-- migrasi skema SAJA, diambil dari DATABASE_SCHEMA.md §5.22/5.23. CRUD/
-- execution engine sungguhan menyusul S4W-10/11/12 (H14-19) -- lihat
-- implementation_gaps.md untuk catatan penyimpangan di bawah.
--
-- Penyimpangan dari DATABASE_SCHEMA.md §5.22 (disengaja, konsisten dengan
-- kebijakan standing "semua hard delete -> soft delete" yang sudah
-- ditegakkan di SETIAP tabel entitas lain di codebase ini -- organizations/
-- workspaces/projects/dst, walau backlog.md US-051 AC menulis "penghapusan
-- bersifat permanen"): kolom `deleted_at` ditambah, TIDAK ada di dokumen
-- schema asli. Keputusan penghapusan sungguhan (soft vs hard) tetap
-- ditentukan ulang saat S4W-10 (CRUD) dikerjakan -- migrasi ini cuma
-- menyediakan kolomnya supaya tidak perlu migrasi susulan kalau soft-delete
-- yang akhirnya dipilih (pola sama pertimbangan di setiap tabel lain).
--
-- Polymorphic FK scope_type/scope_id -- PERSIS pola custom_statuses (§5.11,
-- migrasi 20260924090000): tidak di-enforce FK di level database,
-- integritas dijaga application layer. RLS REUSE fungsi existing
-- (prodo_is_group_admin_of_workspace/prodo_is_workspace_member/
-- prodo_is_project_member) -- TIDAK bikin fungsi baru, konsisten cakupan
-- custom_statuses persis (project-scope HANYA project_member, tidak ada
-- bypass GA-of-project/workspace-member-of-project -- rule project-scoped
-- sengaja privat ke member project itu saja, sama seperti custom_statuses
-- project-scoped).
CREATE TABLE automation_rules (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  scope_type       VARCHAR(20) NOT NULL CHECK (scope_type IN ('workspace', 'project')),
  scope_id         UUID NOT NULL,
  name             VARCHAR(255) NOT NULL,
  trigger_config   JSONB NOT NULL,
  condition_config JSONB,
  action_config    JSONB NOT NULL,
  is_active        BOOLEAN NOT NULL DEFAULT TRUE,
  is_template      BOOLEAN NOT NULL DEFAULT FALSE,
  created_by       UUID NOT NULL REFERENCES users(id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at       TIMESTAMPTZ
);

CREATE INDEX idx_automation_rules_scope ON automation_rules (scope_type, scope_id) WHERE deleted_at IS NULL;

ALTER TABLE automation_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_rules FORCE ROW LEVEL SECURITY;

-- DELETE SQL sengaja dikosongkan (pola sama tasks/workspaces/projects,
-- IG-43 ACCEPTED RISK defense-in-depth) -- "hapus" lewat UPDATE deleted_at,
-- tercakup policy _update.
CREATE POLICY automation_rules_select ON automation_rules
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

CREATE POLICY automation_rules_insert ON automation_rules
  FOR INSERT TO prodo_app
  WITH CHECK (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

CREATE POLICY automation_rules_update ON automation_rules
  FOR UPDATE TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR (scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(scope_id) OR prodo_is_workspace_member(scope_id)))
    OR (scope_type = 'project' AND prodo_is_project_member(scope_id))
  );

-- automation_rule_executions (§5.23) -- log immutable (US-052 AC: "bersifat
-- read-only; tidak dapat diedit atau dihapus"), pola PERSIS webhook_deliveries
-- (satu baris per eksekusi, tidak pernah diupdate). `status` sengaja
-- VARCHAR+CHECK dua-nilai (bukan enum job_status di DATABASE_SCHEMA.md
-- §1591, dan bukan 5 nilai pending/running/completed/failed/dead) --
-- konsisten pola webhook_deliveries.status ('delivered'/'failed'): baris
-- cuma ditulis SETELAH satu percobaan selesai (sinkron ATAU job Asynq
-- S4W-11), tidak pernah ada baris berstatus "pending"/"running" di tabel
-- ini. csv_imports (satu-satunya tabel lain yang dokumennya sebut
-- job_status) juga sudah menyimpang jadi VARCHAR(12) polos, sama alasan.
CREATE TABLE automation_rule_executions (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  rule_id       UUID NOT NULL REFERENCES automation_rules(id) ON DELETE CASCADE,
  trigger_event JSONB NOT NULL,
  triggered_by  UUID REFERENCES users(id),
  executed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  status        VARCHAR(12) NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'failed')),
  action_taken  JSONB,
  error_message TEXT
);

CREATE INDEX idx_rule_executions_rule_id ON automation_rule_executions (rule_id, executed_at DESC);

ALTER TABLE automation_rule_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_rule_executions FORCE ROW LEVEL SECURITY;

CREATE POLICY automation_rule_executions_select ON automation_rule_executions
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM automation_rules r WHERE r.id = automation_rule_executions.rule_id
      AND (
        (r.scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(r.scope_id) OR prodo_is_workspace_member(r.scope_id)))
        OR (r.scope_type = 'project' AND prodo_is_project_member(r.scope_id))
      )
  ));

CREATE POLICY automation_rule_executions_insert ON automation_rule_executions
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM automation_rules r WHERE r.id = automation_rule_executions.rule_id
      AND (
        (r.scope_type = 'workspace' AND (prodo_is_group_admin_of_workspace(r.scope_id) OR prodo_is_workspace_member(r.scope_id)))
        OR (r.scope_type = 'project' AND prodo_is_project_member(r.scope_id))
      )
  ));
