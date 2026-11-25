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
-- expires_at is stored as Unix seconds, so this is a plain numeric comparison:
-- no timezone ambiguity, and (unlike wrapping the column in strftime()) it
-- leaves idx_oidc_auth_requests_expires usable. The CAST is on the constant,
-- not the column, for the same reason.
DELETE FROM oidc_auth_requests
WHERE state_hash = ? AND expires_at > CAST(strftime('%s', 'now') AS INTEGER)
RETURNING *;

-- name: DeleteExpiredOIDCAuthRequests :exec
-- Same comparison as ConsumeOIDCAuthRequest: this must never sweep a row that
-- is still live.
DELETE FROM oidc_auth_requests WHERE expires_at <= CAST(strftime('%s', 'now') AS INTEGER);
