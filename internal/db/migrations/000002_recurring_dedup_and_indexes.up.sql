-- Unique partial index to prevent duplicate recurring transaction instances.
-- ON CONFLICT targets this constraint in generate.go.
CREATE UNIQUE INDEX uq_transactions_recurring_occurrence
    ON transactions (user_id, recurring_transaction_id, date)
    WHERE recurring_transaction_id IS NOT NULL;

-- Missing FK indexes for performance
CREATE INDEX idx_group_members_user_id ON group_members(user_id);
CREATE INDEX idx_transactions_recurring_tx_id ON transactions(recurring_transaction_id) WHERE recurring_transaction_id IS NOT NULL;
CREATE INDEX idx_transactions_group_id ON transactions(group_id) WHERE group_id IS NOT NULL;
CREATE INDEX idx_settlements_from_user ON settlements(from_user);
CREATE INDEX idx_settlements_to_user ON settlements(to_user);
CREATE INDEX idx_group_transactions_paid_by ON group_transactions(paid_by_user_id);
