-- S4G-08, Track S4G (implementation_gaps.md IG-39): notifications_select_own
-- (migrations/20260823090100_rls_policies.up.sql) HANYA mengizinkan
-- `user_id = prodo_current_user_id()` -- tidak ada jalur platform_admin
-- sama sekali, beda dari HAMPIR SEMUA policy lain di codebase ini yang
-- selalu punya klausa `OR prodo_is_platform_admin()`. Ketahuan lewat
-- StorageQuotaCheckJob (S4G-08): job berjalan trusted TANPA actor
-- sungguhan (db.SetRLSContext(..., "", "platform_admin"), sama pola
-- InvitationService.AcceptInvitation) -- cek dedup "sudah kirim notif
-- tipe ini hari ini?" SELALU mengembalikan 0 baris walau notifikasi
-- sungguhan ada, karena app.current_user_id kosong tidak match user_id
-- siapa pun. Root cause SAMA seperti IG-01/IG-38: policy owner-only yang
-- belum punya jalur trusted-background/Platform-Admin.
DROP POLICY IF EXISTS notifications_select_own ON notifications;
CREATE POLICY notifications_select_own ON notifications
  FOR SELECT TO prodo_app
  USING (user_id = prodo_current_user_id() OR prodo_is_platform_admin());
