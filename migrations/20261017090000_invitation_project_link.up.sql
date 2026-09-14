-- Pencatatan undangan Project Manager tertaut project tertentu (S4W susulan,
-- dikonfirmasi user 2026-09-13): "Tambah Project" perlu bisa mengundang
-- Project Manager baru lewat email, sama efisien seperti "Tambah/Kelola
-- Workspace" (WorkspaceService.CreateWorkspace jalur admin_workspace_email).
-- BEDA penting dari admin workspace: project WAJIB dibuat langsung meski PM
-- masih undangan pending ("menunggu PM") -- projects.pm_user_id sudah
-- nullable sejak awal (20260909090000), jadi cuma perlu menautkan SATU
-- undangan project_manager ke SATU project tertentu supaya begitu diterima,
-- pm_user_id project itu otomatis terisi (lihat InvitationService.
-- AcceptInvitation + ProjectRepository.AssignPendingPM).
--
-- Kolom ini ORTOGONAL terhadap chk_invitation_shape (20260915090300) --
-- hanya berlaku untuk undangan bentuk workspace (project_id selalu NULL
-- untuk undangan Eksekutif), tidak perlu mengubah constraint itu.
ALTER TABLE user_invitations
  ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX idx_invitations_pending_project ON user_invitations (project_id)
  WHERE project_id IS NOT NULL AND accepted_at IS NULL AND cancelled_at IS NULL;
