-- +goose Up
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Initialize default email template
INSERT INTO settings (key, value) VALUES ('default_email_template', 'Hei!

Du er herved invitert til å delta på: {{.Title}}

Tidspunkt: {{.StartTime}}{{if .EndTime}} - {{.EndTime}}{{end}}
Sted: {{.Location}}

{{.Description}}

Vennligst klikk på knappen nedenfor for å registrere om du kan delta eller ikke.

Svarfrist: {{.Deadline}}

Det er begrenset med plasser, så vi setter pris på raskt svar!');

-- +goose Down
DROP TABLE settings;
