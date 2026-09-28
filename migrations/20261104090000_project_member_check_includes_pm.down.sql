CREATE OR REPLACE FUNCTION prodo_is_project_member(p_project_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE SECURITY DEFINER
AS $$
  SELECT EXISTS (
    SELECT 1 FROM project_members
    WHERE project_id = p_project_id AND user_id = prodo_current_user_id()
  )
$$;
