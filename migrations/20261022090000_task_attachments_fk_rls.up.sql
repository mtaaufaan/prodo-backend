-- S4W-18 (H11), EPIC 10 Attachment Management. `task_attachments` SUDAH
-- ADA sejak migrasi 20260912090000 (forward-pull S4G-06/IG-19, dibuat
-- MINIMAL supaya Workspace menu S4G-05 punya sumber data storage_used) --
-- task_id/comment_id sengaja TANPA FK saat itu karena tabel `tasks`/
-- `task_comments` belum ada, dengan catatan eksplisit "tambahkan FK
-- sungguhan begitu kedua tabel itu dibuat". `tasks` sudah ada (Task
-- Management Core, migrasi 20260924090000) -- migrasi ini menutup separuh
-- itu (task_id) dan menyalakan RLS yang dari awal juga belum ada sama
-- sekali. `task_comments` MASIH belum ada (EPIC 5 Collaboration, comment
-- belum dibangun) -- comment_id TETAP tanpa FK, didokumentasikan lagi di
-- sini supaya tidak terlupa saat epic Comment akhirnya dikerjakan.
--
-- task_id historisnya nullable (skema minimal) padahal DATABASE_SCHEMA.md
-- §5.21 mendokumentasikannya NOT NULL ("comment_id NULL = lampiran di
-- deskripsi task" -- task_id sendiri SELALU wajib, attachment tidak pernah
-- berdiri sendiri tanpa task induk). Tabel masih kosong (fitur upload
-- belum dibangun sama sekali) -- aman diperketat sekarang, bukan menunggu
-- data nyata.
ALTER TABLE task_attachments ALTER COLUMN task_id SET NOT NULL;
ALTER TABLE task_attachments ADD CONSTRAINT fk_task_attachments_task
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;

-- RLS -- akses ikut task induk, pola PERSIS task_assignees_all (migrasi
-- 20260924090000): subquery ke project_id lewat tasks, reuse fungsi yang
-- sama. Sebelum ini task_attachments TIDAK PUNYA RLS sama sekali (gap
-- laten sejak migrasi minimal -- tidak berdampak nyata karena belum ada
-- baris/endpoint yang menyentuhnya, ditutup sekarang sebelum S4W-19
-- membangun upload sungguhan di atasnya).
ALTER TABLE task_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_attachments FORCE ROW LEVEL SECURITY;

CREATE POLICY task_attachments_select ON task_attachments
  FOR SELECT TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = task_attachments.task_id
      AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
  ));

CREATE POLICY task_attachments_insert ON task_attachments
  FOR INSERT TO prodo_app
  WITH CHECK (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = task_attachments.task_id
      AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
  ));

CREATE POLICY task_attachments_update ON task_attachments
  FOR UPDATE TO prodo_app
  USING (prodo_is_platform_admin() OR EXISTS (
    SELECT 1 FROM tasks t WHERE t.id = task_attachments.task_id
      AND (prodo_is_group_admin_of_project(t.project_id) OR prodo_is_workspace_member_of_project(t.project_id) OR prodo_is_project_member(t.project_id))
  ));

-- DELETE SQL sengaja dikosongkan (pola sama tasks -- "hapus" attachment
-- lewat UPDATE deleted_at/kolom rename, US-064b, bukan SQL DELETE
-- sungguhan, tercakup policy _update).
