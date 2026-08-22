DROP POLICY IF EXISTS invitations_update ON user_invitations;
DROP POLICY IF EXISTS invitations_insert ON user_invitations;
DROP POLICY IF EXISTS invitations_select ON user_invitations;
ALTER TABLE user_invitations DISABLE ROW LEVEL SECURITY;
