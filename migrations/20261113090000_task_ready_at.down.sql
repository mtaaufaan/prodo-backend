ALTER TABLE tasks
    DROP COLUMN IF EXISTS completeness_reopen_count,
    DROP COLUMN IF EXISTS ready_at;
