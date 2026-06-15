package testutil

import (
	"database/sql"

	"digesterr/internal/schema"

	_ "modernc.org/sqlite"
)

func OpenTestDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema.DDL); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
