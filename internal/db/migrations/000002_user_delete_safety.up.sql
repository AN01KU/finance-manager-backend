-- ============================================================================
-- Migration 000002: user-delete safety + schema cleanup
--
-- Groups of changes:
-- 1. Add users.monthly_budget; drop monthly_budgets table
-- 2. Drop admin_audit_log table
-- 3. custom_categories: replace deleted_at with is_deleted boolean
-- 4. Relax FKs on group_transactions.paid_by_user_id,
--    settlements.from_user/to_user, and group_transaction_splits.user_id
--    from CASCADE to SET NULL + drop NOT NULL constraint so a user hard-delete
--    no longer cascades through group financial history.
-- ============================================================================

-- 1. Budget: move per-user scalar onto users row
ALTER TABLE users
    ADD COLUMN monthly_budget DECIMAL(12,2) CHECK (monthly_budget IS NULL OR monthly_budget >= 0);

-- Copy existing budgets: take the most-recent row per user.
UPDATE users u
SET monthly_budget = mb.budget_limit
FROM (
    SELECT DISTINCT ON (user_id)
           user_id, budget_limit
    FROM monthly_budgets
    ORDER BY user_id, year DESC, month DESC
) mb
WHERE u.id = mb.user_id;

DROP TABLE monthly_budgets;

-- 2. Drop audit log (unused)
DROP TABLE IF EXISTS admin_audit_log;

-- 3. custom_categories: deleted_at → is_deleted
ALTER TABLE custom_categories
    ADD COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE custom_categories
    SET is_deleted = TRUE
    WHERE deleted_at IS NOT NULL;

ALTER TABLE custom_categories DROP COLUMN deleted_at;

-- 4a. group_transactions.paid_by_user_id: CASCADE → SET NULL, allow NULL
ALTER TABLE group_transactions
    DROP CONSTRAINT group_transactions_paid_by_user_id_fkey;

ALTER TABLE group_transactions
    ALTER COLUMN paid_by_user_id DROP NOT NULL;

ALTER TABLE group_transactions
    ADD CONSTRAINT group_transactions_paid_by_user_id_fkey
    FOREIGN KEY (paid_by_user_id) REFERENCES users(id) ON DELETE SET NULL;

-- 4b. group_transaction_splits.user_id: CASCADE → SET NULL, allow NULL
ALTER TABLE group_transaction_splits
    DROP CONSTRAINT group_transaction_splits_user_id_fkey;

-- The UNIQUE(group_transaction_id, user_id) constraint must be replaced so
-- NULLs (multiple deleted-user splits) don't violate uniqueness.
-- PostgreSQL NULLs are distinct in UNIQUE constraints, so this is fine as-is,
-- but we drop NOT NULL anyway.
ALTER TABLE group_transaction_splits
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE group_transaction_splits
    ADD CONSTRAINT group_transaction_splits_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

-- 4c. settlements.from_user: CASCADE → SET NULL, allow NULL
ALTER TABLE settlements
    DROP CONSTRAINT settlements_from_user_fkey;

ALTER TABLE settlements
    ALTER COLUMN from_user DROP NOT NULL;

ALTER TABLE settlements
    ADD CONSTRAINT settlements_from_user_fkey
    FOREIGN KEY (from_user) REFERENCES users(id) ON DELETE SET NULL;

-- 4d. settlements.to_user: CASCADE → SET NULL, allow NULL
ALTER TABLE settlements
    DROP CONSTRAINT settlements_to_user_fkey;

ALTER TABLE settlements
    ALTER COLUMN to_user DROP NOT NULL;

ALTER TABLE settlements
    ADD CONSTRAINT settlements_to_user_fkey
    FOREIGN KEY (to_user) REFERENCES users(id) ON DELETE SET NULL;
