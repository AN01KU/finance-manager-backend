-- Add username to users table
ALTER TABLE users ADD COLUMN username VARCHAR(50) UNIQUE;
UPDATE users SET username = SUBSTRING(email FROM 1 FOR POSITION('@' IN email) - 1) WHERE username IS NULL;
ALTER TABLE users ALTER COLUMN username SET NOT NULL;

-- Change category_id to category in personal_expenses (drop and recreate)
ALTER TABLE personal_expenses DROP COLUMN IF EXISTS category_id;
ALTER TABLE personal_expenses ADD COLUMN category VARCHAR(50);
