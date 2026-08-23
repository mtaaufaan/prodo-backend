DROP POLICY IF EXISTS workspaces_delete ON workspaces;
DROP POLICY IF EXISTS workspaces_update ON workspaces;
DROP POLICY IF EXISTS workspaces_insert ON workspaces;
DROP POLICY IF EXISTS workspaces_select ON workspaces;
ALTER TABLE workspaces DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS orgs_delete ON organizations;
DROP POLICY IF EXISTS orgs_update ON organizations;
DROP POLICY IF EXISTS orgs_insert ON organizations;
DROP POLICY IF EXISTS orgs_select ON organizations;
ALTER TABLE organizations DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS prodo_is_group_admin_of_group(UUID);
DROP FUNCTION IF EXISTS prodo_is_group_admin_of_org(UUID);
