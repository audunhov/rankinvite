-- name: GetInvitation :one
SELECT * FROM invitations
WHERE id = ? LIMIT 1;

-- name: ListInvitations :many
SELECT * FROM invitations;

-- name: SaveInvitation :exec
INSERT INTO invitations (id, data) VALUES (?, ?)
ON CONFLICT(id) DO UPDATE SET data = excluded.data;

-- name: GetAdmin :one
SELECT * FROM admins
WHERE username = ? LIMIT 1;

-- name: CreateAdmin :exec
INSERT INTO admins (id, username, password_hash)
VALUES (?, ?, ?);

-- name: AdminExists :one
SELECT EXISTS(SELECT 1 FROM admins WHERE username = ?);

-- name: GetAllParticipants :many
SELECT DISTINCT json_extract(pi.value, '$.participant_email') as email
FROM invitations, json_each(json_extract(invitations.data, '$.personal_invites')) as pi;

-- name: CreateSession :exec
INSERT INTO sessions (id, username, expires_at)
VALUES (?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions
WHERE id = ? LIMIT 1;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < ?;
