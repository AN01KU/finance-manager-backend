-- Add soft-delete support to groups table
ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;

-- Index for filtering non-deleted groups
CREATE INDEX IF NOT EXISTS idx_groups_not_deleted ON groups(is_deleted) WHERE is_deleted = FALSE;
