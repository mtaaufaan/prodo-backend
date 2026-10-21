-- S7A-10 (IG-156): batalkan pembacaan organisasi oleh anggota project-scoped.
DROP POLICY IF EXISTS orgs_select_project_scoped ON organizations;
