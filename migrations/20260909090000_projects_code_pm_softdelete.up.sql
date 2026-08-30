-- S4-01/02/03 (US-012): 3 kolom yang belum ada di §5.12 tapi wajib menurut
-- desain asli "AW Add Project.dc.html"/"AW Projects.dc.html" (dikonfirmasi
-- user 2026-08-30, di luar acuan skema S3 H9 forward-pull):
--   - code: prefiks nomor task project ("RIL-001"), tetap setelah dibuat,
--     unik per workspace (bukan global -- project di workspace lain boleh
--     pakai kode sama, sesuai logic AW Add Project.dc.html yang cuma cek
--     dalam daftar project workspace aktif).
--   - pm_user_id: PM penanggung jawab, WAJIB diisi saat create (§7.1
--     project_scoped_role TIDAK punya nilai 'project_manager' -- PM adalah
--     workspace_role, bukan project-scoped, jadi tidak bisa direpresentasikan
--     lewat project_members, perlu kolom sendiri di projects).
--   - deleted_at/purge_scheduled_at: desain asli menyebut hapus project
--     sebagai soft-delete dengan retensi + bisa dipulihkan Group Admin
--     (BUKAN hard-delete seperti WorkspaceRepository.Delete) -- pola sama
--     organizations.purge_scheduled_at (§5.7): dihitung saat delete
--     (NOW() + organizations.retention_days), job purge otomatis belum
--     dibangun (gap didokumentasikan, bukan dikerjakan sekarang).
ALTER TABLE projects
  ADD COLUMN code VARCHAR(5),
  ADD COLUMN pm_user_id UUID REFERENCES users(id),
  ADD COLUMN deleted_at TIMESTAMPTZ,
  ADD COLUMN purge_scheduled_at TIMESTAMPTZ;

ALTER TABLE projects
  ADD CONSTRAINT ck_projects_code_format CHECK (code IS NULL OR code ~ '^[A-Z]{2,5}$'),
  ADD CONSTRAINT uq_projects_workspace_code UNIQUE (workspace_id, code);

CREATE INDEX idx_projects_active ON projects (workspace_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_projects_purge ON projects (purge_scheduled_at) WHERE purge_scheduled_at IS NOT NULL;
