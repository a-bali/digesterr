package httpapi

import (
	"database/sql"
	"strconv"

	"digesterr/internal/testutil"
)

func openTestDB() (*sql.DB, error) {
	return testutil.OpenTestDB()
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
