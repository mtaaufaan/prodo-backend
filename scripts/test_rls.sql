-- S2-10/11 RLS smoke test -- pola sama dengan RLS_DESIGN.md §11.1, tapi
-- data dibuat sendiri di dalam transaksi (bukan bergantung ke seed data
-- spesifik satu environment) supaya bisa dijalankan di mana saja lalu
-- di-ROLLBACK, tidak meninggalkan jejak.
--
-- Jalankan: docker exec -i <container-postgres> psql -U prodo -d prodo_dev -f - < scripts/test_rls.sql
-- (atau psql "$DATABASE_URL" -f scripts/test_rls.sql kalau psql lokal ada)
--
-- Cakupan: hanya tabel yang SUDAH di-RLS di S2 (workspace_members,
-- notifications, audit_logs) -- lihat implementation_gaps.md IG-10 untuk
-- tabel yang sengaja belum di-RLS (organizations/workspaces/dst, belum
-- ada handler yang query langsung).
BEGIN;

INSERT INTO groups (id, name, tier) VALUES ('11111111-aaaa-0000-0000-000000000001', 'Test RLS Group', 'starter');
INSERT INTO organizations (id, group_id, name, slug) VALUES ('11111111-aaaa-0000-0000-000000000002', '11111111-aaaa-0000-0000-000000000001', 'Test RLS Org', 'test-rls-org');
INSERT INTO workspaces (id, org_id, name) VALUES ('11111111-aaaa-0000-0000-000000000003', '11111111-aaaa-0000-0000-000000000002', 'Test RLS Workspace');

INSERT INTO users (id, email, display_name, platform_role) VALUES
  ('11111111-aaaa-0000-0000-0000000000a1', 'rls-member@test.local', 'RLS Member', 'member'),
  ('11111111-aaaa-0000-0000-0000000000a2', 'rls-other@test.local', 'RLS Other', 'member');
INSERT INTO workspace_members (workspace_id, user_id, role) VALUES
  ('11111111-aaaa-0000-0000-000000000003', '11111111-aaaa-0000-0000-0000000000a1', 'admin_workspace');

SET ROLE prodo_app_user;

-- TEST 1: user acak (bukan member workspace ini) tidak lihat baris apapun.
SELECT set_config('app.current_user_id', '11111111-aaaa-0000-0000-0000000000a2', true);
SELECT set_config('app.current_platform_role', 'member', true);
DO $$
DECLARE v_count INT;
BEGIN
  SELECT COUNT(*) INTO v_count FROM workspace_members WHERE workspace_id = '11111111-aaaa-0000-0000-000000000003';
  ASSERT v_count = 0, 'TEST 1 GAGAL: non-member seharusnya lihat 0 baris workspace_members';
  RAISE NOTICE 'TEST 1 PASS: non-member tidak lihat workspace_members workspace lain';
END $$;

-- TEST 2: member sungguhan lihat baris workspace_members-nya sendiri.
SELECT set_config('app.current_user_id', '11111111-aaaa-0000-0000-0000000000a1', true);
DO $$
DECLARE v_count INT;
BEGIN
  SELECT COUNT(*) INTO v_count FROM workspace_members WHERE workspace_id = '11111111-aaaa-0000-0000-000000000003';
  ASSERT v_count = 1, 'TEST 2 GAGAL: member seharusnya lihat 1 baris workspace_members';
  RAISE NOTICE 'TEST 2 PASS: member sungguhan lihat workspace_members mereka sendiri';
END $$;

-- TEST 3: platform_admin bypass (walau bukan member workspace manapun).
SELECT set_config('app.current_user_id', '11111111-aaaa-0000-0000-0000000000a2', true);
SELECT set_config('app.current_platform_role', 'platform_admin', true);
DO $$
DECLARE v_count INT;
BEGIN
  SELECT COUNT(*) INTO v_count FROM workspace_members WHERE workspace_id = '11111111-aaaa-0000-0000-000000000003';
  ASSERT v_count = 1, 'TEST 3 GAGAL: platform_admin seharusnya bypass dan lihat semua baris';
  RAISE NOTICE 'TEST 3 PASS: platform_admin bypass workspace_members';
END $$;

-- TEST 4: notifikasi lintas-user (actor insert notif UNTUK user lain, pola
-- role-changed di WorkspaceMemberRepository.AssignRole) harus BOLEH.
SELECT set_config('app.current_platform_role', 'member', true);
SELECT set_config('app.current_user_id', '11111111-aaaa-0000-0000-0000000000a1', true);
DO $$
BEGIN
  INSERT INTO notifications (user_id, actor_id, type, title, body)
  VALUES ('11111111-aaaa-0000-0000-0000000000a2', '11111111-aaaa-0000-0000-0000000000a1', 'role_changed', 'test', 'test');
  RAISE NOTICE 'TEST 4 PASS: insert notifikasi lintas-user (actor != user_id target) berhasil';
END $$;

-- TEST 5: actor TIDAK BOLEH lihat notifikasi milik user lain (isolasi Owner SELECT).
DO $$
DECLARE v_count INT;
BEGIN
  SELECT COUNT(*) INTO v_count FROM notifications WHERE user_id = '11111111-aaaa-0000-0000-0000000000a2';
  ASSERT v_count = 0, 'TEST 5 GAGAL: actor seharusnya TIDAK bisa lihat notifikasi milik user lain';
  RAISE NOTICE 'TEST 5 PASS: isolasi notifications SELECT (Owner-only) berfungsi';
END $$;

-- TEST 6: audit_logs insert (org_id NULL, belum ada org context) tetap
-- boleh -- mencakup partition routing (audit_logs adalah partitioned table).
DO $$
BEGIN
  INSERT INTO audit_logs (actor_id, actor_role, action, entity_type, entity_id)
  VALUES ('11111111-aaaa-0000-0000-0000000000a1', 'admin_workspace', 'test.rls_check', 'test', gen_random_uuid());
  RAISE NOTICE 'TEST 6 PASS: insert audit_logs (org_id NULL) berhasil lewat partition routing';
END $$;

-- TEST 7: audit_logs immutable -- UPDATE tidak boleh mengubah apapun.
-- CATATAN: RLS_DESIGN.md §11.1 mengasumsikan UPDATE tanpa policy yang
-- cocok melempar EXCEPTION insufficient_privilege -- perilaku Postgres
-- sesungguhnya (dikonfirmasi live) adalah UPDATE diam-diam match 0 baris
-- (default-deny bekerja seperti klausa WHERE false tambahan).
DO $$
DECLARE v_updated INT;
BEGIN
  UPDATE audit_logs SET action = 'TAMPERED' WHERE action = 'test.rls_check';
  GET DIAGNOSTICS v_updated = ROW_COUNT;
  ASSERT v_updated = 0, 'TEST 7 GAGAL: UPDATE audit_logs seharusnya match 0 baris, tapi match ' || v_updated;
  RAISE NOTICE 'TEST 7 PASS: UPDATE audit_logs match 0 baris (tidak ada policy UPDATE, default-deny)';
END $$;

RESET ROLE;
ROLLBACK;
