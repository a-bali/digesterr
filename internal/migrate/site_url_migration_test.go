package migrate

import (
	"testing"
)

// feeds.site_url is added by ALTER TABLE on an existing database, so the case
// that matters is an upgrade, not a fresh install: existing rows must gain the
// column with its default and keep their data, and a second Run must not retry
// the ALTER (which would fail — SQLite has no ADD COLUMN IF NOT EXISTS).
func TestMigration45AddsSiteURLToExistingFeeds(t *testing.T) {
	db := openTestDB(t)
	if err := Run(db); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	// Simulate the pre-v45 shape: drop the version record and the column.
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 45"); err != nil {
		t.Fatalf("clear version: %v", err)
	}
	if _, err := db.Exec("ALTER TABLE feeds DROP COLUMN site_url"); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash) VALUES (1, 'a@x', 'h')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO feeds (user_id, type, name, url) VALUES (1, 'rss', 'Kept Feed', 'https://example.com/feed')`); err != nil {
		t.Fatalf("seed feed: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("upgrade Run: %v", err)
	}

	var siteURL, name, url string
	if err := db.QueryRow("SELECT site_url, name, url FROM feeds WHERE id = 1").Scan(&siteURL, &name, &url); err != nil {
		t.Fatalf("read feed: %v", err)
	}
	if siteURL != "" {
		t.Errorf("site_url = %q on an upgraded row, want the empty default", siteURL)
	}
	if name != "Kept Feed" || url != "https://example.com/feed" {
		t.Errorf("existing row was altered: name=%q url=%q", name, url)
	}

	// Second Run: the version is recorded, so the ALTER is not replayed.
	if err := Run(db); err != nil {
		t.Fatalf("second Run must be a no-op, got: %v", err)
	}
}
