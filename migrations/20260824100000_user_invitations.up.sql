-- S2-16, US-006. Kolom persis DATABASE_SCHEMA.md §5.30 -- token_hash
-- (SHA-256, bukan token plaintext), TANPA kolom status tunggal (state
-- pending/accepted/cancelled diturunkan dari kombinasi accepted_at/
-- cancelled_at, lihat S2-21).
CREATE TABLE user_invitations (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email        VARCHAR(320) NOT NULL,
  workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  role         workspace_role NOT NULL,
  invited_by   UUID NOT NULL REFERENCES users(id),
  token_hash   TEXT NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  accepted_at  TIMESTAMPTZ,
  cancelled_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Satu undangan pending per email per workspace (DATABASE_SCHEMA.md §5.30
-- "Constraints") -- re-invite email yang sama boleh kapan pun undangan
-- sebelumnya sudah expired/accepted/cancelled.
CREATE UNIQUE INDEX uq_invitation_pending
  ON user_invitations (workspace_id, email)
  WHERE accepted_at IS NULL AND cancelled_at IS NULL;

CREATE INDEX idx_user_invitations_workspace_id ON user_invitations (workspace_id);
CREATE INDEX idx_user_invitations_invited_by ON user_invitations (invited_by);
