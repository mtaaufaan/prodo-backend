DROP TABLE IF EXISTS project_imports;
DROP INDEX IF EXISTS idx_sprints_project_code;
ALTER TABLE sprints DROP COLUMN IF EXISTS code;
