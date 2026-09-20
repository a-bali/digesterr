package migrate

import (
	"database/sql"
	"log/slog"
)

// runInvariants enforces the two load-bearing uniqueness invariants that live
// outside the migration loop: a unique index cannot be created while colliding
// rows exist, so these check for collisions first and report them instead of
// failing quietly. They run after every Run, including on fresh databases.
func runInvariants(db *sql.DB) error {
	ensureEmailFeedURLUnique(db)
	ensureUserEmailInboxUnique(db)
	return nil
}

// ensureEmailFeedURLUnique creates the instance-wide unique index on email
// feed addresses, reporting collisions instead of failing quietly.
//
// An email address is a routing key: two feeds on the same plus-address both
// read the same mail, which double-ingests every newsletter. Email feeds are
// namespaced per account (only one account can hold a given inbox — see the
// users.email_inbox index), so a global index on url is consistent with the
// per-user setting and costs no migration.
//
// Duplicate rows can only arrive by hand-editing the DB, so this reports them
// and leaves them alone rather than deleting a feed: the operator decides.
func ensureEmailFeedURLUnique(db *sql.DB) {
	var dupes int
	err := db.QueryRow(`
SELECT count(*) FROM (
    SELECT url FROM feeds WHERE type = 'email' AND url != ''
    GROUP BY url HAVING count(*) > 1
)`).Scan(&dupes)
	if err != nil {
		slog.Warn("email feed url uniqueness check failed", "error", err)
		return
	}
	if dupes > 0 {
		// Name the addresses but not the owners: this is a log, and the rows
		// may belong to more than one account.
		rows, qerr := db.Query(`
SELECT url, count(*) FROM feeds WHERE type = 'email' AND url != ''
GROUP BY url HAVING count(*) > 1 ORDER BY url`)
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var url string
				var n int
				if rows.Scan(&url, &n) == nil {
					slog.Warn("duplicate email feed address", "url", url, "feeds", n)
				}
			}
		}
		slog.Warn("unique index on email feed addresses NOT created: resolve the duplicate addresses and restart", "duplicate_addresses", dupes)
		return
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_feeds_email_url ON feeds(url) WHERE type = 'email'"); err != nil {
		slog.Warn("failed to create unique index on email feed addresses", "error", err)
	}
}

// ensureUserEmailInboxUnique creates the unique index that makes an AgentMail
// inbox belong to exactly one account, reporting collisions instead of failing
// quietly.
//
// It is the load-bearing invariant of the per-user email configuration: mail is
// staged in email_messages per inbox, and a feed reads the rows addressed to its
// own plus-address, so two accounts sharing a mailbox would each read the other's
// subscriptions. Without the index nothing rejects that — the second account's
// save simply succeeds and the two silently share one mailbox.
func ensureUserEmailInboxUnique(db *sql.DB) {
	var dupes int
	err := db.QueryRow(`
SELECT count(*) FROM (
    SELECT email_inbox FROM users WHERE email_inbox != ''
    GROUP BY email_inbox HAVING count(*) > 1
)`).Scan(&dupes)
	if err != nil {
		slog.Warn("user email inbox uniqueness check failed", "error", err)
		return
	}
	if dupes > 0 {
		rows, qerr := db.Query(`
SELECT email_inbox, count(*) FROM users WHERE email_inbox != ''
GROUP BY email_inbox HAVING count(*) > 1 ORDER BY email_inbox`)
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var inbox string
				var n int
				if rows.Scan(&inbox, &n) == nil {
					// The inbox is the operator's own configuration, not
					// another account's private data, so naming it is useful
					// here; no user identity is logged.
					slog.Warn("duplicate email inbox across accounts", "inbox", inbox, "accounts", n)
				}
			}
		}
		slog.Warn("unique index on users.email_inbox NOT created: give each account its own mailbox and restart", "duplicate_inboxes", dupes)
		return
	}
	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_inbox ON users(email_inbox) WHERE email_inbox != ''"); err != nil {
		slog.Warn("failed to create unique index on users.email_inbox", "error", err)
	}
}
