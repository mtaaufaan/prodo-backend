DROP POLICY IF EXISTS pm_delete ON project_members;
DROP POLICY IF EXISTS pm_update ON project_members;
DROP POLICY IF EXISTS pm_insert ON project_members;
DROP POLICY IF EXISTS pm_select ON project_members;
ALTER TABLE project_members DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS projects_delete ON projects;
DROP POLICY IF EXISTS projects_update ON projects;
DROP POLICY IF EXISTS projects_insert ON projects;
DROP POLICY IF EXISTS projects_select ON projects;
ALTER TABLE projects DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS prodo_is_project_member(UUID);
DROP FUNCTION IF EXISTS prodo_is_workspace_member_of_project(UUID);
DROP FUNCTION IF EXISTS prodo_is_group_admin_of_project(UUID);
