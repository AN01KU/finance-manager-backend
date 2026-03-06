-- Remove category column from expenses table
ALTER TABLE expenses DROP COLUMN IF EXISTS category;
