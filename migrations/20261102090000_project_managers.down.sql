-- Lossy by nature (many-to-one collapse): kalau sebuah project sempat
-- punya lebih dari satu PM, cuma yang PALING AWAL ditambahkan yang
-- dipertahankan di pm_user_id -- sama seperti down migration lain di repo
-- ini untuk perubahan struktural besar, TIDAK menjamin round-trip data
-- lossless, cuma menjamin skema kembali valid.
ALTER TABLE projects ADD COLUMN pm_user_id UUID REFERENCES users(id);

UPDATE projects p
SET pm_user_id = pmg.user_id
FROM (
  SELECT DISTINCT ON (project_id) project_id, user_id
  FROM project_managers
  ORDER BY project_id, added_at ASC
) pmg
WHERE pmg.project_id = p.id;

DROP TABLE project_managers;
