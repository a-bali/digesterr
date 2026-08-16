package store

import "database/sql"

// DB returns the underlying database handle as *sql.DB, used by the health
// endpoint to check connectivity. The Queries type is always constructed with
// a *sql.DB in main; the assertion cannot fail in practice.
//
// Kept in its own file so `sqlc generate` (which rewrites db.go) never
// deletes it.
func (q *Queries) DB() *sql.DB {
	if db, ok := q.db.(*sql.DB); ok {
		return db
	}
	return nil
}
