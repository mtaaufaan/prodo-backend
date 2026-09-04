DROP POLICY IF EXISTS notifications_select_own ON notifications;
CREATE POLICY notifications_select_own ON notifications
  FOR SELECT TO prodo_app
  USING (user_id = prodo_current_user_id());
