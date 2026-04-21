DROP INDEX IF EXISTS idx_groups_not_deleted;
ALTER TABLE groups DROP COLUMN IF EXISTS is_deleted;
