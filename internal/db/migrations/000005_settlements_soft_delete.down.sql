DROP INDEX IF EXISTS idx_groups_created_by_not_deleted;
DROP INDEX IF EXISTS idx_settlements_not_deleted;
ALTER TABLE settlements DROP COLUMN IF EXISTS is_deleted;
ALTER TABLE settlements DROP COLUMN IF EXISTS updated_at;
