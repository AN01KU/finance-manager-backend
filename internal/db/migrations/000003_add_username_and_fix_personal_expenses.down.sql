-- Remove username from users table
ALTER TABLE users DROP COLUMN IF EXISTS username;

-- Revert personal_expenses category change
ALTER TABLE personal_expenses DROP COLUMN IF EXISTS category;
ALTER TABLE personal_expenses ADD COLUMN category_id UUID REFERENCES expense_categories(id);
