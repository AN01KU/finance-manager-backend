-- ============================================================================
-- Phase 1: Recurring transactions correctness
--
-- Adds:
--   1. users.timezone (IANA tz name, e.g. "Asia/Kolkata") so recurring
--      occurrences fire at the user's local midnight, not UTC.
--   2. recurring_skipped_occurrences — when a user manually deletes a
--      generated recurring transaction, we record the skipped date so the
--      backfill loop never regenerates it.
--   3. One-off bump of last_added_date on existing active rules so the new
--      backfill loop does not flood already-onboarded users with historical
--      transactions on first deploy.
-- ============================================================================

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT 'UTC';

CREATE TABLE IF NOT EXISTS recurring_skipped_occurrences (
    recurring_transaction_id UUID NOT NULL REFERENCES recurring_transactions(id) ON DELETE CASCADE,
    occurrence_date          DATE NOT NULL,
    skipped_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (recurring_transaction_id, occurrence_date)
);

CREATE INDEX IF NOT EXISTS idx_recurring_skipped_by_rule
    ON recurring_skipped_occurrences(recurring_transaction_id);

-- One-off backfill protection: bump last_added_date on every currently active
-- rule whose last_added_date is in the past (or NULL). Without this, the new
-- "generate every missed occurrence" loop would create one row per missed day
-- the next time the scheduler ticks.
UPDATE recurring_transactions
SET last_added_date = NOW()
WHERE is_active = TRUE
  AND (last_added_date IS NULL OR last_added_date < NOW());
