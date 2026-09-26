-- invitation_project_scoped_only (IG-100 susulan, "AW Invite Member.dc.html"
-- actingRole='Project Manager'/"PM Member Project.dc.html" tombol "+
-- MEMBER") -- PM menambah project-scoped member (editor/approver/viewer)
-- lewat email langsung, TERMASUK dari luar workspace/organisasi ini.
-- Berbeda dari undangan AW existing (yang SELALU menjadikan invitee
-- workspace_members juga): undangan project_scoped_only=TRUE, saat
-- diterima, TIDAK membuat baris workspace_members sama sekali -- cuma
-- project_members(is_scoped=TRUE). Default FALSE menjaga SEMUA undangan
-- lama/existing (AW invite member workspace) tetap berperilaku PERSIS
-- sama seperti sebelumnya.
ALTER TABLE user_invitations
  ADD COLUMN project_scoped_only BOOLEAN NOT NULL DEFAULT FALSE;
