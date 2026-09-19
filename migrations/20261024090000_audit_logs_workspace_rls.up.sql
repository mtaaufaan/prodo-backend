-- Audit Trail Workspace (S4W-16/17, US-058) -- audit_logs sudah punya
-- kolom workspace_id sejak skema awal, tapi RLS SELECT yang ada
-- (audit_logs_select_group_admin, migrasi 20260922090000) HANYA mengizinkan
-- platform_admin/group_admin. Admin Workspace TIDAK PERNAH bisa membaca
-- audit_logs sama sekali sampai policy ini ada.
--
-- Dual-clause (kolom asli ATAU metadata->>'workspace_id') -- PERSIS pola
-- groupScopeClause (repository/group_audit_repository.go): insertRuleAudit/
-- insertWebhookAudit menulis workspace_id ke metadata JSON, BUKAN kolom
-- audit_logs.workspace_id asli (tabel itu tidak dirancang untuk ditulis
-- lewat chokepoint generik writeAuditLog yang tidak punya parameter kolom
-- workspace_id) -- kalau policy ini cuma cek kolom asli, baris rule/webhook
-- akan tidak pernah terlihat oleh Admin Workspace mana pun.
--
-- group_admin turut disertakan (bukan cuma admin_workspace) -- konsisten
-- pola context-switch GA ke workspace bypass filter role (WorkspaceLayout,
-- middleware.RequireRole) yang sudah berlaku di seluruh fitur AW lain.
CREATE POLICY audit_logs_select_workspace_admin ON audit_logs FOR SELECT TO prodo_app
USING (
  (workspace_id IS NOT NULL AND (prodo_is_workspace_member(workspace_id) OR prodo_is_group_admin_of_workspace(workspace_id)))
  OR (
    (metadata ? 'workspace_id') AND (metadata->>'workspace_id') IS NOT NULL
    AND (prodo_is_workspace_member((metadata->>'workspace_id')::uuid) OR prodo_is_group_admin_of_workspace((metadata->>'workspace_id')::uuid))
  )
);
