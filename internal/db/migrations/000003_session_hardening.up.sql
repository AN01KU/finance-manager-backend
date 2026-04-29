-- ============================================================================
-- Phase 6: Session hardening
--
-- Adds:
--   1. users.tokens_invalidated_after — JWT revocation cutoff; any token
--      whose `iat` <= this timestamp is rejected without a token store.
--      Bumped to NOW() on logout, password change, and email change.
--   2. idx_sync_sessions_active — partial index on active sync sessions
--      (WHERE invalidated_at IS NULL) for fast lookups in ValidateSession,
--      SyncSessionGuard, and TTL cleanup.
-- ============================================================================

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS tokens_invalidated_after TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_sync_sessions_active
    ON sync_sessions(id) WHERE invalidated_at IS NULL;
