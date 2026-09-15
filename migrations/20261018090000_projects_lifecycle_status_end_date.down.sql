ALTER TABLE projects
  DROP COLUMN end_date,
  DROP COLUMN status;

DROP TYPE project_lifecycle_status;
