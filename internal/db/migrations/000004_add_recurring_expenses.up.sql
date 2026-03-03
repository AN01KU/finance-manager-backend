-- Add recurring expense fields to personal_expenses
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS is_recurring BOOLEAN DEFAULT false;
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS frequency VARCHAR(20);
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS day_of_month INTEGER;
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS recurring_end_date TIMESTAMP WITH TIME ZONE;
ALTER TABLE personal_expenses ADD COLUMN IF NOT EXISTS is_active BOOLEAN DEFAULT true;
