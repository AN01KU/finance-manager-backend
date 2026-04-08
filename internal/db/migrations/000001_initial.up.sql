-- Users
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    username VARCHAR(100) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Recurring transactions (must come before transactions due to FK)
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

-- Personal transactions (expenses + income)
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

-- Monthly budgets
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

CREATE INDEX idx_monthly_budgets_user ON monthly_budgets(user_id, year, month);

-- Custom categories
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

-- Groups
CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

-- Group transactions (master record: who paid, full amount)
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

-- FK from transactions back to groups
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_group
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE SET NULL;

-- FK from transactions back to group_transactions
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_group_tx
    FOREIGN KEY (group_transaction_id) REFERENCES group_transactions(id) ON DELETE SET NULL;

-- Settlements
CREATE TABLE settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    from_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (from_user != to_user)
);

CREATE INDEX idx_settlements_group ON settlements(group_id);

-- FK from transactions back to settlements
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_settlement
    FOREIGN KEY (settlement_id) REFERENCES settlements(id) ON DELETE SET NULL;

-- Sync sessions (offline-first sync support)
CREATE TABLE sync_sessions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    invalidated_at      TIMESTAMPTZ,
    invalidation_reason TEXT
);

CREATE INDEX idx_sync_sessions_user_id ON sync_sessions(user_id);
