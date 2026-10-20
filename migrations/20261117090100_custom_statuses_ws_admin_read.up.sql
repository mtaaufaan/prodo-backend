-- Admin Workspace dapat MEMBACA status project (S7A-04, implementation_gaps.md IG-150).
--
-- Temuan probe live: custom_statuses_select untuk scope 'project' hanya meloloskan anggota project / PM
-- (prodo_is_project_member), sehingga Admin Workspace yang bukan anggota project melihat 0 status project padahal
-- melihat semua task workspace-nya. Akibatnya Board kosong dan SEMUA jalur yang membaca status (ubah status,
-- keputusan approval) gagal "status tidak ditemukan" bagi Admin Workspace. Hanya SELECT yang dibuka; tulis status
-- project tetap lewat kebijakan yang ada.
CREATE OR REPLACE FUNCTION prodo_is_workspace_admin_of_project(p_project_id UUID)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER AS $$
  SELECT EXISTS (
    SELECT 1
    FROM projects p
    JOIN workspace_members wm ON wm.workspace_id = p.workspace_id
    WHERE p.id = p_project_id
      AND wm.user_id = prodo_current_user_id()
      AND wm.role = 'admin_workspace'
  )
$$;

CREATE POLICY custom_statuses_select_ws_admin ON custom_statuses
  FOR SELECT TO prodo_app
  USING (scope_type = 'project' AND prodo_is_workspace_admin_of_project(scope_id));
