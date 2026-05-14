-- Migration 000003: relax groups.created_by FK
--
-- Migration 002 relaxed FKs on group_transactions.paid_by_user_id,
-- group_transaction_splits.user_id, and settlements.from_user/to_user
-- but missed groups.created_by, which was still NOT NULL ... ON DELETE CASCADE.
-- That meant DELETE FROM users would cascade into soft-deleted groups and
-- destroy other users' financial history. Change to ON DELETE SET NULL so
-- user deletion preserves group records.
ALTER TABLE groups
    DROP CONSTRAINT groups_created_by_fkey;

ALTER TABLE groups
    ALTER COLUMN created_by DROP NOT NULL;

ALTER TABLE groups
    ADD CONSTRAINT groups_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
