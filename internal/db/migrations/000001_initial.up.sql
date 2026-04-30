-- ============================================================================
-- Users
-- ============================================================================
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    username VARCHAR(100) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'INR',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    -- tokens_invalidated_after is the cutoff used by JWTAuth to revoke
    -- previously-issued JWTs without storing them server-side: any token
    -- whose `iat` is <= this value is rejected as stale. Bumped to NOW()
    -- on logout, password change, and email change.
    tokens_invalidated_after TIMESTAMPTZ,
    -- Per-account login throttle: incremented on each bcrypt mismatch,
    -- reset on success. When attempts hits the threshold the account is
    -- locked until login_locked_until passes. Defends against credential
    -- stuffing via botnets that bypass per-IP rate limiting.
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    login_locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Case-insensitive email uniqueness (defense-in-depth alongside app-level normalization).
CREATE UNIQUE INDEX users_email_lower_idx ON users (LOWER(email));

-- ============================================================================
-- Email verification codes
-- ============================================================================
CREATE TABLE email_verifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code VARCHAR(6) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_verifications_user_id ON email_verifications(user_id);
CREATE INDEX idx_email_verifications_lookup ON email_verifications(user_id, code) WHERE used_at IS NULL;

-- ============================================================================
-- Recurring transactions (must come before transactions due to FK)
-- ============================================================================
CREATE TABLE recurring_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(10) NOT NULL DEFAULT 'expense' CHECK (type IN ('expense', 'income')),
    name VARCHAR(255) NOT NULL,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    category VARCHAR(100) NOT NULL,
    frequency VARCHAR(20) NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    day_of_month INTEGER CHECK (day_of_month >= 1 AND day_of_month <= 31),
    days_of_week INTEGER[],
    start_date TIMESTAMPTZ NOT NULL,
    end_date TIMESTAMPTZ,
    is_active BOOLEAN DEFAULT TRUE,
    last_added_date TIMESTAMPTZ,
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_recurring_transactions_user_id ON recurring_transactions(user_id);
CREATE INDEX idx_recurring_transactions_active ON recurring_transactions(user_id, is_active) WHERE is_active = TRUE;

-- ============================================================================
-- Personal transactions (expenses + income)
-- ============================================================================
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(10) NOT NULL CHECK (type IN ('expense', 'income')),
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    category VARCHAR(100) NOT NULL,
    date TIMESTAMPTZ NOT NULL,
    description VARCHAR(255),
    notes TEXT,
    recurring_transaction_id UUID REFERENCES recurring_transactions(id) ON DELETE SET NULL,
    group_transaction_id UUID,
    group_id UUID,
    settlement_id UUID,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_transactions_user_id ON transactions(user_id);
CREATE INDEX idx_transactions_date ON transactions(user_id, date DESC);
CREATE INDEX idx_transactions_type ON transactions(user_id, type);
CREATE INDEX idx_transactions_not_deleted ON transactions(user_id, is_deleted) WHERE is_deleted = FALSE;
CREATE INDEX idx_transactions_settlement ON transactions(settlement_id) WHERE settlement_id IS NOT NULL;
CREATE INDEX idx_transactions_group_tx_id ON transactions(group_transaction_id) WHERE group_transaction_id IS NOT NULL;
CREATE INDEX idx_transactions_recurring_tx_id ON transactions(recurring_transaction_id) WHERE recurring_transaction_id IS NOT NULL;
CREATE INDEX idx_transactions_group_id ON transactions(group_id) WHERE group_id IS NOT NULL;

-- Deduplicate recurring transaction instances
CREATE UNIQUE INDEX uq_transactions_recurring_occurrence
    ON transactions (user_id, recurring_transaction_id, date)
    WHERE recurring_transaction_id IS NOT NULL;

-- ============================================================================
-- Monthly budgets
-- ============================================================================
CREATE TABLE monthly_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year INTEGER NOT NULL CHECK (year >= 2000 AND year <= 2100),
    month INTEGER NOT NULL CHECK (month >= 1 AND month <= 12),
    budget_limit DECIMAL(12,2) NOT NULL CHECK (budget_limit >= 0),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, year, month)
);

-- ============================================================================
-- Custom categories
-- ============================================================================
CREATE TABLE custom_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    icon VARCHAR(100) NOT NULL,
    color VARCHAR(7) NOT NULL,
    is_hidden BOOLEAN DEFAULT FALSE,
    is_predefined BOOLEAN DEFAULT FALSE,
    predefined_key VARCHAR(50),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, name)
);

CREATE INDEX idx_custom_categories_user_id ON custom_categories(user_id);
CREATE UNIQUE INDEX idx_custom_categories_user_predefined_key ON custom_categories(user_id, predefined_key) WHERE predefined_key IS NOT NULL;

-- ============================================================================
-- Groups
-- ============================================================================
CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_groups_not_deleted ON groups(is_deleted) WHERE is_deleted = FALSE;
CREATE INDEX idx_groups_created_by_not_deleted ON groups(created_by, is_deleted) WHERE is_deleted = FALSE;

CREATE TABLE group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

CREATE INDEX idx_group_members_user_id ON group_members(user_id);

-- ============================================================================
-- Group transactions (master record: who paid, full amount)
-- ============================================================================
CREATE TABLE group_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    paid_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_amount DECIMAL(12,2) NOT NULL CHECK (total_amount > 0),
    category VARCHAR(100) NOT NULL,
    date TIMESTAMPTZ NOT NULL,
    description VARCHAR(255),
    notes TEXT,
    is_deleted BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_group_transactions_group ON group_transactions(group_id);
CREATE INDEX idx_group_transactions_not_deleted ON group_transactions(group_id, is_deleted) WHERE is_deleted = FALSE;
CREATE INDEX idx_group_transactions_paid_by ON group_transactions(paid_by_user_id);

-- Group transaction splits (each member's share)
CREATE TABLE group_transaction_splits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_transaction_id UUID NOT NULL REFERENCES group_transactions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount >= 0),
    transaction_id UUID REFERENCES transactions(id) ON DELETE SET NULL,
    UNIQUE(group_transaction_id, user_id)
);

CREATE INDEX idx_group_splits_group_tx ON group_transaction_splits(group_transaction_id);
CREATE INDEX idx_group_splits_user ON group_transaction_splits(user_id);

-- FKs from transactions back to groups / group_transactions
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_group
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE SET NULL;

ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_group_tx
    FOREIGN KEY (group_transaction_id) REFERENCES group_transactions(id) ON DELETE SET NULL;

-- ============================================================================
-- Settlements
-- ============================================================================
CREATE TABLE settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    from_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    notes TEXT,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (from_user != to_user)
);

CREATE INDEX idx_settlements_group ON settlements(group_id);
CREATE INDEX idx_settlements_not_deleted ON settlements(group_id, is_deleted) WHERE is_deleted = FALSE;
CREATE INDEX idx_settlements_from_user ON settlements(from_user);
CREATE INDEX idx_settlements_to_user ON settlements(to_user);

ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_settlement
    FOREIGN KEY (settlement_id) REFERENCES settlements(id) ON DELETE SET NULL;

-- ============================================================================
-- Sync sessions (offline-first sync support)
-- ============================================================================
CREATE TABLE sync_sessions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    invalidated_at      TIMESTAMPTZ,
    invalidation_reason TEXT
);

CREATE INDEX idx_sync_sessions_user_id ON sync_sessions(user_id);
-- Partial index for fast lookup of active sessions; matches every read in
-- ValidateSession / SyncSessionGuard / TTL cleanup.
CREATE INDEX idx_sync_sessions_active ON sync_sessions(id) WHERE invalidated_at IS NULL;

-- ============================================================================
-- Device tokens (push notifications)
-- ============================================================================
CREATE TABLE device_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL,
    platform VARCHAR(10) NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, token)
);

CREATE INDEX idx_device_tokens_user_id ON device_tokens(user_id);
CREATE UNIQUE INDEX idx_device_tokens_token ON device_tokens(token);

-- ============================================================================
-- Admin audit log
-- ============================================================================
CREATE TABLE admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action TEXT NOT NULL,
    table_name TEXT,
    details TEXT,
    admin_user TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_admin_audit_log_created_at ON admin_audit_log(created_at DESC);
