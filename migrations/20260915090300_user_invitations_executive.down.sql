DROP INDEX IF EXISTS idx_invitations_pending_executive;

ALTER TABLE user_invitations
  DROP CONSTRAINT IF EXISTS chk_invitation_shape,
  DROP COLUMN IF EXISTS is_executive_invite,
  DROP COLUMN IF EXISTS group_id,
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN role SET NOT NULL;
