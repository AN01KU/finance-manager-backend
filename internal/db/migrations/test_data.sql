-- Test data for development
-- Run this file after migrations to populate test data

-- Create test users (password is "password123" hashed with bcrypt)
-- For testing, we'll use a simple placeholder hash
INSERT INTO users (id, email, username, password_hash) VALUES
    ('11111111-1111-1111-1111-111111111111', 'alice@example.com', 'alice', '$2a$10$rVnKGJsLd7fOSD.jeF1L5eX8xqXQzY5xY5xY5xY5xY5xY5xY5xY'),
    ('22222222-2222-2222-2222-222222222222', 'bob@example.com', 'bob', '$2a$10$rVnKGJsLd7fOSD.jeF1L5eX8xqXQzY5xY5xY5xY5xY5xY5xY5xY'),
    ('33333333-3333-3333-3333-333333333333', 'charlie@example.com', 'charlie', '$2a$10$rVnKGJsLd7fOSD.jeF1L5eX8xqXQzY5xY5xY5xY5xY5xY5xY5xY')
ON CONFLICT (email) DO NOTHING;

-- Create test groups
INSERT INTO groups (id, name, created_by) VALUES
    ('aaaa1111-aaaa-1111-aaaa-111111111111', 'Trip to Goa', '11111111-1111-1111-1111-111111111111'),
    ('bbbb2222-bbbb-2222-bbbb-222222222222', 'House Expenses', '11111111-1111-1111-1111-111111111111')
ON CONFLICT (id) DO NOTHING;

-- Add members to groups
INSERT INTO group_members (group_id, user_id) VALUES
    ('aaaa1111-aaaa-1111-aaaa-111111111111', '11111111-1111-1111-1111-111111111111'),
    ('aaaa1111-aaaa-1111-aaaa-111111111111', '22222222-2222-2222-2222-222222222222'),
    ('aaaa1111-aaaa-1111-aaaa-111111111111', '33333333-3333-3333-3333-333333333333'),
    ('bbbb2222-bbbb-2222-bbbb-222222222222', '11111111-1111-1111-1111-111111111111'),
    ('bbbb2222-bbbb-2222-bbbb-222222222222', '22222222-2222-2222-2222-222222222222')
ON CONFLICT DO NOTHING;

-- Create expenses for Trip to Goa group
-- Expense 1: Flight tickets (alice paid, split 3 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp1111-1111-1111-1111-111111111111', 'aaaa1111-aaaa-1111-aaaa-111111111111', 'Flight tickets to Goa', 9000.00, '11111111-1111-1111-1111-111111111111');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp1111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111111', 3000.00),
    ('exp1111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222', 3000.00),
    ('exp1111-1111-1111-1111-111111111111', '33333333-3333-3333-3333-333333333333', 3000.00);

-- Expense 2: Hotel booking (bob paid, split 3 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp2222-2222-2222-2222-222222222222', 'aaaa1111-aaaa-1111-aaaa-111111111111', 'Hotel booking - 3 nights', 6000.00, '22222222-2222-2222-2222-222222222222');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp2222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 2000.00),
    ('exp2222-2222-2222-2222-222222222222', '22222222-2222-2222-2222-222222222222', 2000.00),
    ('exp2222-2222-2222-2222-222222222222', '33333333-3333-3333-3333-333333333333', 2000.00);

-- Expense 3: Dinner at beach (charlie paid, split 3 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp3333-3333-3333-3333-333333333333', 'aaaa1111-aaaa-1111-aaaa-111111111111', 'Beach dinner', 1500.00, '33333333-3333-3333-3333-333333333333');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp3333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 500.00),
    ('exp3333-3333-3333-3333-333333333333', '22222222-2222-2222-2222-222222222222', 500.00),
    ('exp3333-3333-3333-3333-333333333333', '33333333-3333-3333-3333-333333333333', 500.00);

-- Expense 4: Taxi and transport (alice paid, split 2 ways - alice & bob)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp4444-4444-4444-4444-444444444444', 'aaaa1111-aaaa-1111-aaaa-111111111111', 'Taxi and local transport', 800.00, '11111111-1111-1111-1111-111111111111');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp4444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 400.00),
    ('exp4444-4444-4444-4444-444444444444', '22222222-2222-2222-2222-222222222222', 400.00);

-- Create expenses for House Expenses group
-- Expense 5: Electricity bill (alice paid, split 2 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp5555-5555-5555-5555-555555555555', 'bbbb2222-bbbb-2222-bbbb-222222222222', 'Monthly electricity bill', 2000.00, '11111111-1111-1111-1111-111111111111');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp5555-5555-5555-5555-555555555555', '11111111-1111-1111-1111-111111111111', 1000.00),
    ('exp5555-5555-5555-5555-555555555555', '22222222-2222-2222-2222-222222222222', 1000.00);

-- Expense 6: Groceries (bob paid, split 2 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp6666-6666-6666-6666-666666666666', 'bbbb2222-bbbb-2222-bbbb-222222222222', 'Weekly groceries', 1500.00, '22222222-2222-2222-2222-222222222222');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp6666-6666-6666-6666-666666666666', '11111111-1111-1111-1111-111111111111', 750.00),
    ('exp6666-6666-6666-6666-666666666666', '22222222-2222-2222-2222-222222222222', 750.00);

-- Expense 7: Internet bill (alice paid, split 2 ways)
INSERT INTO expenses (id, group_id, description, total_amount, paid_by) VALUES
    ('exp7777-7777-7777-7777-777777777777', 'bbbb2222-bbbb-2222-bbbb-222222222222', 'Monthly internet', 1000.00, '11111111-1111-1111-1111-111111111111');
INSERT INTO expense_splits (expense_id, user_id, amount) VALUES
    ('exp7777-7777-7777-7777-777777777777', '11111111-1111-1111-1111-111111111111', 500.00),
    ('exp7777-7777-7777-7777-777777777777', '22222222-2222-2222-2222-222222222222', 500.00);

-- Create a settlement (bob pays alice back for hotel)
INSERT INTO settlements (id, group_id, from_user, to_user, amount) VALUES
    ('settle1-1111-1111-1111-111111111111', 'aaaa1111-aaaa-1111-aaaa-111111111111', '22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 2000.00);

-- Create expense categories for alice
INSERT INTO expense_categories (id, user_id, name, color, icon) VALUES
    ('cat1111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111111', 'Food', '#FF5733', 'restaurant'),
    ('cat2222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'Transport', '#33FF57', 'car'),
    ('cat3333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'Shopping', '#3357FF', 'shopping_bag'),
    ('cat4444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'Entertainment', '#FF33F5', 'movie')
ON CONFLICT DO NOTHING;

-- Create expense categories for bob
INSERT INTO expense_categories (id, user_id, name, color, icon) VALUES
    ('cat5555-5555-5555-5555-555555555555', '22222222-2222-2222-2222-222222222222', 'Food', '#FF5733', 'restaurant'),
    ('cat6666-6666-6666-6666-666666666666', '22222222-2222-2222-2222-222222222222', 'Rent', '#33FF57', 'home')
ON CONFLICT DO NOTHING;

-- Create monthly budgets for alice (February 2026)
INSERT INTO monthly_budgets (id, user_id, amount, month, year) VALUES
    ('budget1-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111111', 5000.00, 2, 2026);

-- Create monthly budgets for bob (February 2026)
INSERT INTO monthly_budgets (id, user_id, amount, month, year) VALUES
    ('budget2-2222-2222-2222-222222222222', '22222222-2222-2222-2222-222222222222', 3000.00, 2, 2026);

-- Create personal expenses for alice
INSERT INTO personal_expenses (id, user_id, category, amount, description, notes, expense_date) VALUES
    ('pe1111-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111111', 'Food', 250.00, 'Lunch at office', 'Healthy salad', '2026-02-01'),
    ('pe2222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'Transport', 150.00, 'Uber to meeting', 'Client visit', '2026-02-03'),
    ('pe3333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'Shopping', 2000.00, 'New headphones', 'Sony WH-1000XM5', '2026-02-05'),
    ('pe4444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'Food', 450.00, 'Dinner with friends', 'Italian restaurant', '2026-02-07'),
    ('pe5555-5555-5555-5555-555555555555', '11111111-1111-1111-1111-111111111111', 'Entertainment', 500.00, 'Movie tickets', 'Matrix Resurrections', '2026-02-10'),
    ('pe6666-6666-6666-6666-666666666666', '11111111-1111-1111-1111-111111111111', 'Transport', 80.00, 'Metro pass', 'Monthly pass', '2026-02-12'),
    ('pe7777-7777-7777-7777-777777777777', '11111111-1111-1111-1111-111111111111', 'Food', 180.00, 'Coffee and snacks', 'Work from cafe', '2026-02-14'),
    ('pe8888-8888-8888-8888-888888888888', '11111111-1111-1111-1111-111111111111', 'Shopping', 350.00, 'Gym membership', 'Annual plan', '2026-02-15');

-- Create personal expenses for bob
INSERT INTO personal_expenses (id, user_id, category, amount, description, notes, expense_date) VALUES
    ('peaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', '22222222-2222-2222-2222-222222222222', 'Food', 300.00, 'Breakfast and lunch', 'Work day', '2026-02-02'),
    ('pebbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb', '22222222-2222-2222-2222-222222222222', 'Rent', 15000.00, 'February rent', 'Paid to landlord', '2026-02-01'),
    ('peccc-cccc-cccc-cccc-cccccccccccc', '22222222-2222-2222-2222-222222222222', 'Food', 200.00, 'Dinner', 'Quick bite', '2026-02-04'),
    ('peddd-dddd-dddd-dddd-dddddddddddd', '22222222-2222-2222-2222-222222222222', 'Food', 150.00, 'Lunch', 'Office cafeteria', '2026-02-06');
