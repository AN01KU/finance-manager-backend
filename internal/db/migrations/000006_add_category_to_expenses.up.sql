-- Add category column to expenses table
ALTER TABLE expenses ADD COLUMN IF NOT EXISTS category VARCHAR(100);
