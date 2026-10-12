DROP POLICY IF EXISTS time_entries_delete_estimate ON time_entries;
DROP POLICY IF EXISTS time_entries_insert_estimate ON time_entries;
DROP INDEX IF EXISTS idx_time_entries_estimate;
ALTER TABLE time_entries DROP COLUMN IF EXISTS is_estimate;
ALTER TABLE tasks DROP COLUMN IF EXISTS is_imported;
