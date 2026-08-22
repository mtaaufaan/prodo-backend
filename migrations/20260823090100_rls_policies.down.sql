ALTER TABLE audit_logs_2027 DISABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2026 DISABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs_2025 DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS audit_logs_insert_app ON audit_logs;
ALTER TABLE audit_logs DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS notifications_delete_own ON notifications;
DROP POLICY IF EXISTS notifications_update_own ON notifications;
DROP POLICY IF EXISTS notifications_insert_app ON notifications;
DROP POLICY IF EXISTS notifications_select_own ON notifications;
ALTER TABLE notifications DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS wm_delete ON workspace_members;
DROP POLICY IF EXISTS wm_update ON workspace_members;
DROP POLICY IF EXISTS wm_insert ON workspace_members;
DROP POLICY IF EXISTS wm_select ON workspace_members;
ALTER TABLE workspace_members DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS prodo_is_workspace_member(UUID);
DROP FUNCTION IF EXISTS prodo_is_platform_admin();
DROP FUNCTION IF EXISTS prodo_platform_role();
DROP FUNCTION IF EXISTS prodo_current_user_id();
