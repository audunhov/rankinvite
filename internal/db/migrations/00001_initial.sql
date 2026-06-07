-- +goose Up
CREATE TABLE invitations (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL
);

CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    csrf_token TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Initialize settings
INSERT INTO settings (key, value) VALUES ('default_email_template', 'Hei!

Du er herved invitert til å delta på: {{.Title}}

Tidspunkt: {{.StartTime}}{{if .EndTime}} - {{.EndTime}}{{end}}
Sted: {{.Location}}

{{.Description}}

Vennligst klikk på knappen nedenfor for å registrere om du kan delta eller ikke.

Svarfrist: {{.Deadline}}

Det er begrenset med plasser, så vi setter pris på raskt svar!');

INSERT INTO settings (key, value) VALUES ('global_sender_name', 'RankInvite System');
INSERT INTO settings (key, value) VALUES ('global_sender_email', 'noreply@rankinvite.no');
INSERT INTO settings (key, value) VALUES ('smtp_host', 'localhost');
INSERT INTO settings (key, value) VALUES ('smtp_port', '1025');
INSERT INTO settings (key, value) VALUES ('smtp_user', '');
INSERT INTO settings (key, value) VALUES ('smtp_pass', '');
INSERT INTO settings (key, value) VALUES ('shared_senders', '');

-- +goose Down
DROP TABLE invitations;
DROP TABLE admins;
DROP TABLE sessions;
DROP TABLE settings;
