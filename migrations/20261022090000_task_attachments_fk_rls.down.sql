DROP POLICY task_attachments_update ON task_attachments;
DROP POLICY task_attachments_insert ON task_attachments;
DROP POLICY task_attachments_select ON task_attachments;
ALTER TABLE task_attachments DISABLE ROW LEVEL SECURITY;

ALTER TABLE task_attachments DROP CONSTRAINT fk_task_attachments_task;
ALTER TABLE task_attachments ALTER COLUMN task_id DROP NOT NULL;
