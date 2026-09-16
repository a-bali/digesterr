CREATE TABLE users (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    email       TEXT    NOT NULL UNIQUE,
    password_hash TEXT  NOT NULL,
    api_key     TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now'))
);

CREATE TABLE feeds (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id             INTEGER NOT NULL,
    category            TEXT    NOT NULL DEFAULT '',
    type                TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    url                 TEXT    NOT NULL DEFAULT '',
    config              TEXT    NOT NULL DEFAULT '{}',
    min_score           INTEGER NOT NULL DEFAULT 0,
    last_fetched_at     DATETIME,
    status              TEXT    NOT NULL DEFAULT 'active',
    extract_content     INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT    NOT NULL DEFAULT '',
    last_error_at       DATETIME,
    last_error_severity TEXT    NOT NULL DEFAULT '',
    created_at          DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at          DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id         INTEGER NOT NULL,
    hash            TEXT    NOT NULL DEFAULT '',
    title           TEXT    NOT NULL,
    url             TEXT    NOT NULL DEFAULT '',
    content         TEXT    NOT NULL DEFAULT '',
    author          TEXT    NOT NULL DEFAULT '',
    comment_url     TEXT    NOT NULL DEFAULT '',
    points          INTEGER NOT NULL DEFAULT 0,
    comments_count  INTEGER NOT NULL DEFAULT 0,
    published_at    DATETIME,
    status          TEXT    NOT NULL DEFAULT 'unread',
    fetched_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    created_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    last_enriched_at DATETIME,
    extract_content  TEXT    NOT NULL DEFAULT '',
    extract_status   TEXT    NOT NULL DEFAULT '',
    extract_retries  INTEGER NOT NULL DEFAULT 0,
    extract_date     DATETIME,
    FOREIGN KEY (feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TABLE digest_configs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL,
    name         TEXT    NOT NULL DEFAULT '',
    schedule     TEXT    NOT NULL,
    config       TEXT    NOT NULL DEFAULT '{}',
    last_error   TEXT    NOT NULL DEFAULT '',
    last_error_at DATETIME,
    created_at   DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at   DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE digest_results (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,
    config_id       INTEGER NOT NULL,
    config_snapshot TEXT    NOT NULL DEFAULT '{}',
    content         TEXT    NOT NULL DEFAULT '{}',
    status          TEXT    NOT NULL DEFAULT 'new',
    created_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at      DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (config_id) REFERENCES digest_configs(id) ON DELETE CASCADE
);

CREATE TABLE digest_result_items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    result_id   INTEGER NOT NULL,
    item_id     INTEGER NOT NULL,
    is_highlight INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (result_id) REFERENCES digest_results(id) ON DELETE CASCADE,
    FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
);

CREATE TABLE refresh_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    token_hash  TEXT    NOT NULL UNIQUE,
    expires_at  DATETIME NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    updated_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- email_messages stages inbound email from the AgentMail inbox before it is
-- turned into feed items. One inbox serves the whole instance and feeds are
-- distinguished by plus-addressing, so a message is routed to a feed by
-- matching its recipient address against feeds.url (see internal/feed/email_address.go).
--
-- Staging rather than fetching per feed is deliberate: several email feeds
-- share one mailbox, so polling the mailbox once per feed per cycle would
-- re-download every message N times. The sync stage drains the mailbox into
-- this table; each EmailFetcher then reads only its own rows.
--
-- The timestamp columns are TEXT, not DATETIME, and hold the API's RFC3339
-- string verbatim (normalised to a fixed layout on ingest). That is
-- deliberate: a Go time.Time bound through sql.NullTime lands in SQLite as
-- "2026-09-15 12:00:00 +0000 UTC" while the API and any RFC3339 literal are
-- "2026-09-15T12:00:00.000Z", and the two never compare equal as text
-- (measured: 1 of 2 rows matched). The poll window is a TEXT comparison, so a
-- typed time here would silently match nothing and the feed would never
-- update. Fixed-width RFC3339 in UTC sorts lexicographically by time.
CREATE TABLE email_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id    TEXT    NOT NULL,
    message_id  TEXT    NOT NULL,              -- RFC 5322 Message-ID, verbatim incl. <>
    thread_id   TEXT    NOT NULL DEFAULT '',
    from_addr   TEXT    NOT NULL DEFAULT '',
    to_addrs    TEXT    NOT NULL DEFAULT '',   -- JSON array of recipient addresses
    subject     TEXT    NOT NULL DEFAULT '',
    text        TEXT    NOT NULL DEFAULT '',
    html        TEXT    NOT NULL DEFAULT '',   -- sanitized on ingest, never raw
    raw_headers TEXT    NOT NULL DEFAULT '{}',
    received_at TEXT,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    -- Idempotency: re-polling an overlapping window must insert nothing new.
    -- This, not the poll watermark, is what makes ingestion exactly-once.
    UNIQUE (inbox_id, message_id)
);

-- email_sync_state tracks the per-inbox poll watermark. One row per inbox.
-- last_synced_at is TEXT for the same reason as email_messages.received_at:
-- it is compared against, and set from, the API's RFC3339 timestamps.
CREATE TABLE email_sync_state (
    inbox_id       TEXT    PRIMARY KEY,
    last_synced_at TEXT,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_at  TEXT,
    updated_at     DATETIME NOT NULL DEFAULT (DATETIME('now'))
);

CREATE INDEX idx_feeds_user_id ON feeds(user_id);
CREATE INDEX idx_feeds_category ON feeds(category);
CREATE INDEX idx_items_feed_id ON items(feed_id);
CREATE INDEX idx_items_status ON items(status);
CREATE INDEX idx_items_created_at ON items(created_at);
CREATE INDEX idx_digest_configs_user_id ON digest_configs(user_id);
CREATE INDEX idx_digest_results_user_id ON digest_results(user_id);
CREATE INDEX idx_digest_results_config_id ON digest_results(config_id);
CREATE INDEX idx_digest_results_created_at ON digest_results(created_at);
CREATE INDEX idx_digest_result_items_result_id ON digest_result_items(result_id);
CREATE INDEX idx_digest_result_items_item_id ON digest_result_items(item_id);
CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_token_hash ON refresh_tokens(token_hash);
CREATE UNIQUE INDEX idx_items_feed_hash ON items(feed_id, hash) WHERE hash != '';
CREATE UNIQUE INDEX idx_users_api_key ON users(api_key) WHERE api_key != '';

-- One AgentMail inbox and its API key are server-wide (like llm.api_key), so the
-- plus-address namespace is shared by every user on the instance. Uniqueness is
-- therefore GLOBAL, not per-user: a per-user index would let two users claim the
-- same suffix and then both read mail addressed to it. Partial, so RSS feeds
-- remain free to duplicate URLs.
CREATE UNIQUE INDEX idx_feeds_email_url ON feeds(url) WHERE type = 'email';

CREATE INDEX idx_email_messages_inbox_received ON email_messages(inbox_id, received_at);