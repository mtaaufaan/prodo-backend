-- Seed: keanggotaan demo Editor (S6E-02, Sprint 6 "Editor Console", IG-133).
--
-- Akun Editor TIDAK dibuat di sini: login butuh user Keycloak, dan itu
-- dibuat lewat alur undangan nyata (Undang Member -> email di Mailpit ->
-- aktivasi/set password). Skrip ini hanya menjadikan user yang SUDAH ada
-- sebagai Editor di sebuah workspace dan anggota sebuah project -- supaya
-- frame Editor punya project untuk ditampilkan.
--
-- Pakai (psql):
--   psql "$DATABASE_URL" -v editor_email="'editor@prodo.local'" \
--        -v workspace_name="'IT - Eldwin'" -v project_name="'QuestLog'" \
--        -f seeds/04_demo_editor.sql
-- Idempotent: aman dijalankan ulang. Tidak mengubah role user yang sudah
-- jadi anggota workspace dengan role lain (ON CONFLICT DO NOTHING).

BEGIN;

INSERT INTO workspace_members (workspace_id, user_id, role)
SELECT w.id, u.id, 'editor'
FROM users u, workspaces w
WHERE u.email = :editor_email AND w.name = :workspace_name
ON CONFLICT (workspace_id, user_id) DO NOTHING;

INSERT INTO project_members (project_id, user_id, role)
SELECT p.id, u.id, 'editor'
FROM users u
JOIN workspaces w ON w.name = :workspace_name
JOIN projects p ON p.workspace_id = w.id AND p.name = :project_name AND p.deleted_at IS NULL
WHERE u.email = :editor_email
ON CONFLICT (project_id, user_id) DO NOTHING;

COMMIT;
