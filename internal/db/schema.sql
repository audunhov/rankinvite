CREATE TABLE invitations (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL
);

CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    csrf_token TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
