-- Revert migration 000003
ALTER TABLE groups
    DROP CONSTRAINT groups_created_by_fkey;

ALTER TABLE groups
    ALTER COLUMN created_by SET NOT NULL;

ALTER TABLE groups
    ADD CONSTRAINT groups_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE;
