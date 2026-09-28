-- Ditemukan user lewat pengujian live Track S5B (IG-101): grid "PM Custom
-- Status" kosong total untuk PM sungguhan padahal DB punya 5 status
-- project. Root cause: prodo_is_project_member() (migrasi 20260829100000)
-- HANYA mengecek project_members, TIDAK PERNAH mengecek project_managers
-- -- gap laten sejak PIC Handoff/story points/timesheet/custom_statuses/
-- automation_rules project-scope dst (11 migrasi memakai fungsi ini),
-- tidak pernah ketahuan sebelumnya karena tasks/sprints/dst SELALU
-- punya OR-clause tambahan prodo_is_workspace_member_of_project yang
-- (secara kebetulan) sudah meloloskan PM lewat workspace_role-nya --
-- custom_statuses/automation_rules project-scope SENGAJA lebih privat
-- (project-member-only, TANPA bypass itu, lihat komentar migrasi
-- 20261021090000) sehingga baru sekarang benar-benar mengetes fungsi
-- ini apa adanya dan gap-nya baru kelihatan.
--
-- Fix di titik tulis bersama (root-cause, bukan tambalan per-tabel) --
-- setiap satu dari 11 RLS policy yang memakai prodo_is_project_member()
-- langsung ikut benar begitu fungsi ini diperbaiki, tidak perlu
-- menyentuh satu pun policy CREATE POLICY yang sudah ada.
CREATE OR REPLACE FUNCTION prodo_is_project_member(p_project_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE SECURITY DEFINER
AS $$
  SELECT EXISTS (
    SELECT 1 FROM project_members
    WHERE project_id = p_project_id AND user_id = prodo_current_user_id()
  ) OR EXISTS (
    SELECT 1 FROM project_managers
    WHERE project_id = p_project_id AND user_id = prodo_current_user_id()
  )
$$;
