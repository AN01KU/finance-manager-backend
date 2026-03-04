-- Add days_of_week column for weekly recurring expenses
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS days_of_week INTEGER[];
