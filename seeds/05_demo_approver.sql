-- Seed: keanggotaan demo Approver (S7A-02, Sprint 7 "Approver Console", IG-148).
--
-- Akun Approver TIDAK dibuat di sini: login butuh user Keycloak, dan itu
-- dibuat lewat alur undangan nyata (Undang Member -> email di Mailpit ->
-- aktivasi/set password). Skrip ini hanya menjadikan user yang SUDAH ada
-- sebagai Approver di sebuah workspace dan anggota sebuah project -- supaya
-- frame Approver punya project untuk ditampilkan (project yang diikuti =
-- anggota project, assignee, atau PIC aktif; sama dengan Editor).
--
-- Pakai (psql):
--   psql "$DATABASE_URL" -v approver_email="'approver@prodo.local'" \
--        -v workspace_name="'IT - Eldwin'" -v project_name="'QuestLog'" \
--        -f seeds/05_demo_approver.sql
-- Idempotent: aman dijalankan ulang. Tidak mengubah role user yang sudah
-- jadi anggota workspace/project dengan role lain (ON CONFLICT DO NOTHING).

BEGIN;

INSERT INTO workspace_members (workspace_id, user_id, role)
SELECT w.id, u.id, 'approver'
FROM users u, workspaces w
WHERE u.email = :approver_email AND w.name = :workspace_name
ON CONFLICT (workspace_id, user_id) DO NOTHING;

INSERT INTO project_members (project_id, user_id, role)
SELECT p.id, u.id, 'approver'
FROM users u
JOIN workspaces w ON w.name = :workspace_name
JOIN projects p ON p.workspace_id = w.id AND p.name = :project_name AND p.deleted_at IS NULL
WHERE u.email = :approver_email
ON CONFLICT (project_id, user_id) DO NOTHING;

COMMIT;
