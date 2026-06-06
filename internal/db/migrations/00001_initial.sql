-- +goose Up
CREATE TABLE invitations (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL
);

CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL
);

-- +goose Down
DROP TABLE invitations;
DROP TABLE admins;
