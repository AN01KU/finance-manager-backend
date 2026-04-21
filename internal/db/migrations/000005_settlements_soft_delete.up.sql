-- Add soft-delete and updated_at to settlements for consistency with other tables
ALTER TABLE settlements ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE settlements ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_settlements_not_deleted ON settlements(group_id, is_deleted) WHERE is_deleted = FALSE;

-- Index for DeleteMe ownership check query
CREATE INDEX IF NOT EXISTS idx_groups_created_by_not_deleted ON groups(created_by, is_deleted) WHERE is_deleted = FALSE;
