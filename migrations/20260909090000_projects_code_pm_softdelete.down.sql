DROP INDEX IF EXISTS idx_projects_purge;
DROP INDEX IF EXISTS idx_projects_active;

ALTER TABLE projects
  DROP CONSTRAINT IF EXISTS uq_projects_workspace_code,
  DROP CONSTRAINT IF EXISTS ck_projects_code_format;

ALTER TABLE projects
  DROP COLUMN purge_scheduled_at,
  DROP COLUMN deleted_at,
  DROP COLUMN pm_user_id,
  DROP COLUMN code;
