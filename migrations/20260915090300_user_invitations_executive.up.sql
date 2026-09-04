-- Members & Roles (Track S4G, forward-pull dari cakupan US-086): perluas
-- user_invitations supaya bisa mengundang email baru langsung sebagai
-- Eksekutif MURNI (tanpa workspace/role sama sekali) -- kasus di desain
-- "GA Members Roles.dc.html" (toggle "Eksekutif" di modal Undang, tanpa
-- pasangan workspace). workspace_id/role jadi nullable, ditambah group_id +
-- is_executive_invite -- CHECK memastikan tepat satu bentuk yang terisi.
ALTER TABLE user_invitations
  ALTER COLUMN workspace_id DROP NOT NULL,
  ALTER COLUMN role DROP NOT NULL,
  ADD COLUMN group_id UUID REFERENCES groups(id) ON DELETE CASCADE,
  ADD COLUMN is_executive_invite BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE user_invitations
  ADD CONSTRAINT chk_invitation_shape CHECK (
    (is_executive_invite = FALSE AND workspace_id IS NOT NULL AND role IS NOT NULL AND group_id IS NULL)
    OR
    (is_executive_invite = TRUE AND workspace_id IS NULL AND role IS NULL AND group_id IS NOT NULL)
  );

-- uq_invitation_pending (workspace_id, email) tidak menjangkau undangan
-- eksekutif (workspace_id selalu NULL di situ, NULL tidak dianggap sama di
-- unique constraint Postgres) -- partial unique index terpisah, pola sama
-- idx_invitations_pending.
CREATE UNIQUE INDEX idx_invitations_pending_executive ON user_invitations (group_id, email)
  WHERE is_executive_invite = TRUE AND accepted_at IS NULL AND cancelled_at IS NULL;
