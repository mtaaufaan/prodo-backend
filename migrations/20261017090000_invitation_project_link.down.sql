DROP INDEX IF EXISTS idx_invitations_pending_project;

ALTER TABLE user_invitations
  DROP COLUMN IF EXISTS project_id;
