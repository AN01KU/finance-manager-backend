-- Add username to users table
ALTER TABLE users ADD COLUMN username VARCHAR(50) UNIQUE NOT NULL;
