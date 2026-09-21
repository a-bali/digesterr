// Package migrate applies the database schema and its ordered migrations.
//
// It replaces the former ad-hoc runner in cmd/digesterr, which re-executed
// every migration on each startup and demoted failures to logs. Here each
// migration runs at most once, is recorded in schema_migrations, and runs
// inside a transaction; an unexpected failure aborts startup instead of being
// swallowed.
//
// Existing databases predating this package have no schema_migrations table.
// They are upgraded by inspecting actual schema state: each migration's
// applied function reports whether the database already reflects it, so a
// stale database gets only the migrations it is missing and a current one is
// baselined without touching rows.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"digesterr/internal/schema"
)

// migration is one ordered schema change.
type migration struct {
	version int
	name    string
	stmts   []string
	// applied reports whether the database already reflects this migration's
	// end state. Used to baseline legacy databases without re-running. Nil
	// means the statements are inherently idempotent (IF NOT EXISTS) and are
	// simply executed once.
	applied func(*sql.DB) (bool, error)
	// disableFK runs the migration with foreign keys off. Required for table
	// rebuilds: SQLite ignores PRAGMA foreign_keys inside a transaction, so the
	// toggle happens on a dedicated connection around it.
	disableFK bool
}

// Run initialises a fresh database (applying the embedded schema) or brings an
// existing one up to date, then enforces post-migration invariants. It returns
// an error if any migration fails, so startup can fail closed.
func Run(db *sql.DB) error {
	ctx := context.Background()

	fresh, err := isFresh(db)
	if err != nil {
		return fmt.Errorf("checking for existing schema: %w", err)
	}
	if fresh {
		if _, err := db.Exec(schema.DDL); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}
		slog.Info("database schema initialized")
	}

	if err := ensureVersionTable(db); err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	// A fresh database gets the embedded schema, which is already the end
	// state of every migration: record them all without running any, so a
	// later binary only has to apply migrations newer than the schema file.
	if fresh {
		for _, m := range migrations {
			if applied[m.version] {
				continue
			}
			if err := recordVersion(db, m); err != nil {
				return err
			}
		}
		return runInvariants(db)
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if m.applied != nil {
			ok, err := m.applied(db)
			if err != nil {
				return fmt.Errorf("checking migration %q: %w", m.name, err)
			}
			if ok {
				slog.Debug("schema migration baselined (already present)", "version", m.version, "name", m.name)
				if err := recordVersion(db, m); err != nil {
					return err
				}
				continue
			}
		}
		if err := runMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration v%d (%s): %w", m.version, m.name, err)
		}
		slog.Info("schema migration applied", "version", m.version, "name", m.name)
	}

	// A failed rebuild can leave enforcement off; make sure it is on.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("re-enabling foreign keys: %w", err)
	}
	return runInvariants(db)
}

// runMigration executes one migration and records its version atomically on a
// dedicated connection, so a PRAGMA foreign_keys toggle cannot leak to a
// different pooled connection. A migration with no statements (baseline-only,
// e.g. legacy table rebuilds) is simply recorded.
func runMigration(ctx context.Context, db *sql.DB, m migration) error {
	if len(m.stmts) == 0 {
		_, err := db.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name)
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if m.disableFK {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		// Restore even on failure, or a returned-to-pool connection would
		// silently enforce nothing for every later query.
		defer conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}

func isFresh(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

func ensureVersionTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT (DATETIME('now'))
)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func recordVersion(db *sql.DB, m migration) error {
	if _, err := db.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		return fmt.Errorf("recording migration v%d: %w", m.version, err)
	}
	return nil
}

// HasColumn reports whether table has the named column. table is always a
// constant from this package; only column is bound.
func HasColumn(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow("SELECT count(*) FROM pragma_table_info('"+table+"') WHERE name = ?", column).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// columnAbsent returns an applied-guard that reports true once the column is
// gone (used by the two feeds-table rebuilds).
func columnAbsent(table, column string) func(*sql.DB) (bool, error) {
	return func(db *sql.DB) (bool, error) {
		has, err := HasColumn(db, table, column)
		if err != nil {
			return false, err
		}
		return !has, nil
	}
}

// columnPresent returns an applied-guard that reports true once the column
// exists.
func columnPresent(table, column string) func(*sql.DB) (bool, error) {
	return func(db *sql.DB) (bool, error) {
		return HasColumn(db, table, column)
	}
}

// noRows returns an applied-guard that reports true when the query matches no
// rows (i.e. the one-time data work is already done).
func noRows(query string, args ...any) func(*sql.DB) (bool, error) {
	return func(db *sql.DB) (bool, error) {
		var n int
		if err := db.QueryRow(query, args...).Scan(&n); err != nil {
			return false, err
		}
		return n == 0, nil
	}
}

// tableAbsent returns an applied-guard that reports true when the named table
// does not exist. Used for migrations touching tables that predate the
// migration set entirely: on a database that old there is nothing to do (and
// executing would error on the missing table).
func tableAbsent(table string) func(*sql.DB) (bool, error) {
	return noRows("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table)
}

// tablePresent returns an applied-guard that reports true when the named
// table exists. The mirror of tableAbsent: for migrations whose statements
// are IF NOT EXISTS / INSERT OR IGNORE idempotent, presence means a prior
// (or schema-file) run already did the work.
func tablePresent(table string) func(*sql.DB) (bool, error) {
	return func(db *sql.DB) (bool, error) {
		absent, err := tableAbsent(table)(db)
		return !absent, err
	}
}
