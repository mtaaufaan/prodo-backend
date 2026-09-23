ALTER TABLE sprints ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE sprints SET is_active = (status = 'active');

ALTER TABLE sprints DROP COLUMN goal;
ALTER TABLE sprints DROP COLUMN status;
DROP TYPE sprint_status;
