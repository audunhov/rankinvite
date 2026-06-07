-- name: GetInvitation :one
SELECT * FROM invitations
WHERE id = ? LIMIT 1;

-- name: ListInvitations :many
SELECT * FROM invitations
ORDER BY rowid DESC
LIMIT ? OFFSET ?;

-- name: ListInvitationsFiltered :many
SELECT * FROM invitations
WHERE (sqlc.arg(query) = '' OR json_extract(data, '$.title') LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(status) = '' OR json_extract(data, '$.status') = sqlc.arg(status))
ORDER BY rowid DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountInvitations :one
SELECT COUNT(*) FROM invitations;

-- name: CountInvitationsFiltered :one
SELECT COUNT(*) FROM invitations
WHERE (sqlc.arg(query) = '' OR json_extract(data, '$.title') LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(status) = '' OR json_extract(data, '$.status') = sqlc.arg(status));

-- name: SaveInvitation :exec
INSERT INTO invitations (id, data) VALUES (?, ?)
ON CONFLICT(id) DO UPDATE SET data = excluded.data;

-- name: DeleteInvitation :exec
DELETE FROM invitations
WHERE id = ?;

-- name: GetAdmin :one
SELECT * FROM admins
WHERE email = ? LIMIT 1;

-- name: ListAdmins :many
SELECT * FROM admins;

-- name: CreateAdmin :exec
INSERT INTO admins (id, email, password_hash)
VALUES (?, ?, ?);

-- name: DeleteAdmin :exec
DELETE FROM admins
WHERE id = ?;

-- name: UpdateAdminPassword :exec
UPDATE admins
SET password_hash = ?
WHERE id = ?;

-- name: AdminExists :one
SELECT EXISTS(SELECT 1 FROM admins WHERE email = ?);

-- name: GetAllParticipants :many
SELECT DISTINCT json_extract(pi.value, '$.participant_email') as email
FROM invitations, json_each(json_extract(invitations.data, '$.personal_invites')) as pi;

-- name: CreateSession :exec
INSERT INTO sessions (id, email, csrf_token, expires_at)
VALUES (?, ?, ?, ?);

-- name: GetSetting :one
SELECT value FROM settings
WHERE key = ? LIMIT 1;

-- name: UpdateSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: GetSession :one
SELECT * FROM sessions
WHERE id = ? LIMIT 1;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < ?;
