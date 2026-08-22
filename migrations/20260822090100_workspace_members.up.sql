-- S2-01/S2-02, US-002: keanggotaan + role user di dalam workspace. Lihat
-- docs/DATABASE_SCHEMA.md §5.10. Role level platform (platform_admin/
-- group_admin/member) sudah punya enum sendiri (user_platform_role, S1-01)
-- -- workspace_role di sini enum TERPISAH, cuma untuk role di dalam satu
-- workspace (5 nilai, bukan gabungan 7 nilai seperti wording awal S2-02).
CREATE TYPE workspace_role AS ENUM ('admin_workspace', 'project_manager', 'editor', 'approver', 'viewer');

CREATE TABLE workspace_members (
  workspace_id  UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role          workspace_role NOT NULL,
  invited_by    UUID REFERENCES users(id),
  joined_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX idx_workspace_members_user_id ON workspace_members (user_id);
CREATE INDEX idx_workspace_members_role ON workspace_members (workspace_id, role);
