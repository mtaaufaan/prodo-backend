DROP TABLE IF EXISTS task_status_sessions;
ALTER TABLE custom_statuses DROP COLUMN IF EXISTS require_start_confirmation;
ALTER TABLE projects DROP COLUMN IF EXISTS allow_editor_story_points;
