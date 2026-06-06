-- +goose Up
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Initialize default email template
INSERT INTO settings (key, value) VALUES ('default_email_template', 'Hei! Du er herved invitert til {{.Title}}. Svar her: {{.URL}}');

-- +goose Down
DROP TABLE settings;
