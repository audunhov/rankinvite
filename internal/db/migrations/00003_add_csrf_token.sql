-- +goose Up
ALTER TABLE sessions ADD COLUMN csrf_token TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite doesn't support DROP COLUMN in older versions easily, 
-- but for goose we just want the state.
-- ALTER TABLE sessions DROP COLUMN csrf_token;
