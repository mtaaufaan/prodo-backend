-- S3-38 (implementation_gaps.md IG-01): relasi many-to-many Group Admin <-> grup.
-- Satu GA bisa mengelola banyak grup; satu grup bisa punya lebih dari satu GA.
-- Kolom persis DATABASE_SCHEMA.md §5.6.
CREATE TABLE group_admin_assignments (
  group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  assigned_by UUID REFERENCES users(id),
  PRIMARY KEY (group_id, user_id)
);

CREATE INDEX idx_group_admin_assignments_user_id ON group_admin_assignments (user_id);
