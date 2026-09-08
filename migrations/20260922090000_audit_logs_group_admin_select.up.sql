-- Audit Trail (S4G-10/11, Track S4G, desain "GA Audit Trail.dc.html").
-- sprint_backlog.md minta tabel BARU `group_audit_logs` + chokepoint tulis
-- terpisah yang di-retrofit ke semua handler GA existing -- dikonfirmasi
-- user untuk TIDAK dipakai (implementation_gaps.md IG-45): `audit_logs`
-- SUDAH otomatis terisi sejak fitur Organisasi/Workspace/Members/Webhook
-- dibangun (lewat insertOrgAudit/insertWorkspaceAudit/insertWebhookAudit),
-- jadi GA Audit Trail dibangun sebagai READ-ONLY di atas tabel yang sudah
-- ada -- TIDAK ADA file existing yang perlu diubah untuk migrasi ini.
--
-- audit_logs sebelumnya TIDAK punya policy SELECT sama sekali (migrasi
-- 20260823090100_rls_policies, sengaja -- "belum ada endpoint baca audit
-- trail"). Policy ini menyaring lewat organizations.group_id (untuk baris
-- ber-org_id) DAN metadata->>'group_id' (fallback untuk webhook cakupan
-- "seluruh grup" yang org_id-nya NULL, lihat webhook_repository.go
-- insertWebhookAudit) -- TIDAK perlu kolom group_id baru di audit_logs.
CREATE POLICY audit_logs_select_group_admin ON audit_logs
  FOR SELECT TO prodo_app
  USING (
    prodo_is_platform_admin()
    OR EXISTS (
      SELECT 1 FROM organizations o
      WHERE o.id = audit_logs.org_id AND prodo_is_group_admin_of_group(o.group_id)
    )
    OR (
      audit_logs.metadata ? 'group_id'
      AND prodo_is_group_admin_of_group((audit_logs.metadata->>'group_id')::uuid)
    )
  );
