-- Anggota project-scoped (project_members/project_managers TANPA baris workspace_members) dapat MEMBACA baris
-- organisasi workspace-nya (S7A-10, implementation_gaps.md IG-156).
--
-- Temuan probe live: orgs_select hanya meloloskan Platform Admin, Group Admin, dan anggota workspace. Editor/Approver
-- project-scoped (mis. akun yang ditambahkan PM hanya ke satu project) tidak melihat organisasinya, sehingga unggah
-- lampiran (kuota organisasi, GetAttachmentQuotaInfo) selalu gagal "organization not found" walau mereka boleh
-- melampirkan di task project yang diikuti. Hanya SELECT yang dibuka; tulis organisasi tetap Group/Platform Admin.
-- Memakai prodo_is_project_member_of_workspace (SECURITY DEFINER, migrasi 20261105090000) -- tanpa rekursi RLS.
CREATE POLICY orgs_select_project_scoped ON organizations
  FOR SELECT TO prodo_app
  USING (
    prodo_platform_role() = 'member'
    AND EXISTS (
      SELECT 1 FROM workspaces w
      WHERE w.org_id = organizations.id AND prodo_is_project_member_of_workspace(w.id)
    )
  );
