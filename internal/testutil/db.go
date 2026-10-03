package testutil

import (
	"database/sql"
	"fmt"
	"sync/atomic"

	"digesterr/internal/schema"

	_ "modernc.org/sqlite"
)

var testDBCounter atomic.Int64

// OpenTestDB returns a handle to a fresh, empty in-memory SQLite database with
// the production schema applied.
//
// The DSN deliberately uses a NAMED shared-cache in-memory database instead of
// bare ":memory:". With modernc.org/sqlite, ":memory:" is scoped to a single
// CONNECTION, while database/sql pools connections: any second query issued in
// parallel opens another, separate, empty database and silently returns no
// rows. Handlers that resolve items concurrently (the Google Reader stream
// paths do, because per-item comment fetching is a network round-trip) hit
// exactly that, and it presents as "rows vanished" rather than as an error —
// an expensive thing to debug. A named shared-cache database gives every pooled
// connection the same data, matching the file-backed behaviour of production.
func OpenTestDB() (*sql.DB, error) {
	name := fmt.Sprintf("testdb_%d", testDBCounter.Add(1))
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_texttotime=true&_time_format=sqlite",
		name,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// A shared-cache in-memory database exists only while at least one
	// connection is open. Retaining idle connections keeps the schema and rows
	// alive for the whole test; otherwise the pool can evict the last
	// connection and silently drop the database.
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(schema.DDL); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
