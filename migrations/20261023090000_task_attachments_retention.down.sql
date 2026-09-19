DROP INDEX IF EXISTS idx_task_attachments_purge;
ALTER TABLE task_attachments DROP COLUMN IF EXISTS purge_scheduled_at;
