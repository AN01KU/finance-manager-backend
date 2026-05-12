-- ============================================================================
-- Rollback migration 000002
-- ============================================================================

-- 4d. Restore settlements.to_user NOT NULL + CASCADE
ALTER TABLE settlements
    DROP CONSTRAINT settlements_to_user_fkey;

ALTER TABLE settlements
    ALTER COLUMN to_user SET NOT NULL;

ALTER TABLE settlements
    ADD CONSTRAINT settlements_to_user_fkey
    FOREIGN KEY (to_user) REFERENCES users(id) ON DELETE CASCADE;

-- 4c. Restore settlements.from_user NOT NULL + CASCADE
ALTER TABLE settlements
    DROP CONSTRAINT settlements_from_user_fkey;

ALTER TABLE settlements
    ALTER COLUMN from_user SET NOT NULL;

ALTER TABLE settlements
    ADD CONSTRAINT settlements_from_user_fkey
    FOREIGN KEY (from_user) REFERENCES users(id) ON DELETE CASCADE;

-- 4b. Restore group_transaction_splits.user_id NOT NULL + CASCADE
ALTER TABLE group_transaction_splits
    DROP CONSTRAINT group_transaction_splits_user_id_fkey;

ALTER TABLE group_transaction_splits
    ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE group_transaction_splits
    ADD CONSTRAINT group_transaction_splits_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- 4a. Restore group_transactions.paid_by_user_id NOT NULL + CASCADE
ALTER TABLE group_transactions
    DROP CONSTRAINT group_transactions_paid_by_user_id_fkey;

ALTER TABLE group_transactions
    ALTER COLUMN paid_by_user_id SET NOT NULL;

ALTER TABLE group_transactions
    ADD CONSTRAINT group_transactions_paid_by_user_id_fkey
    FOREIGN KEY (paid_by_user_id) REFERENCES users(id) ON DELETE CASCADE;

-- 3. Restore custom_categories.deleted_at
ALTER TABLE custom_categories
    ADD COLUMN deleted_at TIMESTAMPTZ;

UPDATE custom_categories
    SET deleted_at = NOW()
    WHERE is_deleted = TRUE;

ALTER TABLE custom_categories DROP COLUMN is_deleted;

-- 2. Restore admin_audit_log
CREATE TABLE admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_username VARCHAR(100) NOT NULL,
    action TEXT NOT NULL,
    target_type VARCHAR(50),
    target_id TEXT,
    details JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_admin_audit_log_created_at ON admin_audit_log(created_at DESC);

-- 1. Restore monthly_budgets; data from users.monthly_budget is not recoverable per-month
CREATE TABLE monthly_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    budget_limit DECIMAL(12,2) NOT NULL CHECK (budget_limit >= 0),
    month INTEGER NOT NULL CHECK (month >= 1 AND month <= 12),
    year INTEGER NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, month, year)
);

ALTER TABLE users DROP COLUMN monthly_budget;
