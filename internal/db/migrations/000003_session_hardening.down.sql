DROP INDEX IF EXISTS idx_sync_sessions_active;

ALTER TABLE users
    DROP COLUMN IF EXISTS tokens_invalidated_after;
