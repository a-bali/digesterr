-- OIDC identity lookups and writes.
--
-- (issuer, subject) is the only identity key. Email is recorded but never
-- used to find an existing link: a provider may reassign an address, and
-- matching on it would hand one person's account to another after that
-- happens.

-- name: GetUserIdentityByIssuerSubject :one
SELECT * FROM user_identities WHERE issuer = ? AND subject = ?;

-- name: GetUserIdentityByUserAndIssuer :one
SELECT * FROM user_identities WHERE user_id = ? AND issuer = ?;

-- name: CreateUserIdentity :one
INSERT INTO user_identities (user_id, issuer, subject, email)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: UpdateUserIdentityEmail :exec
UPDATE user_identities
SET email = ?, updated_at = DATETIME('now')
WHERE id = ?;

-- name: ListAllUsersEmails :many
-- Used to build the case-insensitive match index in Go: SQLite's LOWER() folds
-- ASCII only, so the authoritative comparison happens in the application.
-- Reading only the two columns keeps this cheap.
SELECT id, email FROM users;

-- name: CreateOIDCUserNoPassword :one
-- A user provisioned through OIDC starts with an empty password hash. An empty
-- hash can never match a bcrypt comparison, so the account cannot be signed
-- into with a password until the owner deliberately sets one.
INSERT INTO users (email, password_hash, api_key)
VALUES (?, '', ?)
RETURNING *;

-- name: CreateOIDCAuthRequest :exec
INSERT INTO oidc_auth_requests (state_hash, nonce, code_verifier, binding_hash, return_to, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ConsumeOIDCAuthRequest :one
-- Single-use: the DELETE returns the row it removed, so two concurrent
-- callbacks presenting the same state cannot both read it. An expired row is
-- also deleted but returns nothing, because expires_at is part of the
-- predicate -- the caller cannot tell "expired" from "unknown", and neither
-- should complete a login.
--
-- The comparison goes through strftime('%s', ...) rather than comparing the
-- stored text against DATETIME('now'). Both columns are TEXT, and SQLite
-- compares them lexicographically: DATETIME('now') carries no offset
-- ("2026-11-25 11:08:32") while a Go-written value carries one
-- ("2026-11-25 06:18:32-05:00"), so a deadline ten minutes in the FUTURE sorts
-- before "now" on any host west of UTC and every login fails as "expired".
-- strftime('%s', ...) parses the offset, giving a real instant on both sides.
-- internal/oidc/authrequest_test.go pins both directions.
DELETE FROM oidc_auth_requests
WHERE state_hash = ? AND strftime('%s', expires_at) > strftime('%s', 'now')
RETURNING *;

-- name: DeleteExpiredOIDCAuthRequests :exec
-- Same instant comparison as ConsumeOIDCAuthRequest: this must never sweep a
-- row that is still live in any timezone.
DELETE FROM oidc_auth_requests WHERE strftime('%s', expires_at) <= strftime('%s', 'now');
