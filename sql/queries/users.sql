-- name: CreateUser :one
INSERT INTO users (email, password_hash, api_key)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByAPIKey :one
-- The api_key != '' predicate mirrors the partial unique index
-- idx_users_api_key: without it SQLite cannot use the index and falls back to
-- a full users scan on every authenticated request.
SELECT * FROM users WHERE api_key = ? AND api_key != '';

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateUserAPIKey :one
UPDATE users
SET api_key = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateUserLLMModel :one
UPDATE users
SET llm_model = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateUserEmailConfig :one
-- Stores a user's AgentMail credentials. Both values are written together: the
-- inbox and the token are only useful as a pair (the token alone cannot name a
-- mailbox, the inbox alone cannot be read), and a partial update would leave the
-- account reporting "configured" while failing every poll.
UPDATE users
SET email_inbox = ?, email_api_key = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: GetUserByEmailInbox :one
-- Resolves the account that owns an inbox. Used to reject a second account
-- claiming the same mailbox: email_messages is staged per inbox, so both would
-- read each other's mail.
SELECT * FROM users WHERE email_inbox != '' AND email_inbox = ?;

-- name: ClearUserEmailConfig :exec
-- Removes a user's AgentMail credentials. Any email feeds they own are left in
-- place and become status = 'error' on the next poll, which is where the sidebar
-- shows the failure.
UPDATE users
SET email_inbox = '', email_api_key = '', updated_at = DATETIME('now')
WHERE id = ?;

-- name: ListEmailAccountsInUse :many
-- One row per distinct inbox that has at least one live email feed. The poller
-- drains each inbox ONCE per cycle, which replaces the old single instance-wide
-- sync: it is derived from feeds, not from users, so an account that configured
-- credentials but owns no email feed issues no requests.
--
-- Grouped by inbox and token together, so a token changed in the settings dialog
-- yields a fresh client rather than reusing one bound to the old credential.
--
-- The CAST on MIN(u.id) is required: sqlc infers a bare MIN() as interface{},
-- which is unusable as a struct field. Same trap as the CASE-bound parameters in
-- items.sql (see the skill's sqlc pitfall section).
SELECT
    u.email_inbox AS inbox_id,
    u.email_api_key AS api_key,
    CAST(MIN(u.id) AS INTEGER) AS user_id
FROM users u
JOIN feeds f ON f.user_id = u.id
WHERE f.type = 'email'
  AND f.status IN ('active', 'error')
  AND u.email_inbox != ''
  AND u.email_api_key != ''
GROUP BY u.email_inbox, u.email_api_key;
