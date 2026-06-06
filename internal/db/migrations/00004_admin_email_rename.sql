-- +goose Up
-- Rename username to email in admins table
ALTER TABLE admins RENAME COLUMN username TO email;

-- Rename username to email in sessions table to maintain consistency
ALTER TABLE sessions RENAME COLUMN username TO email;

-- +goose Down
ALTER TABLE admins RENAME COLUMN email TO username;
ALTER TABLE sessions RENAME COLUMN email TO username;
