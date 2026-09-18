package httpapi

import (
	"database/sql"
	"strconv"
	"testing"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func openTestDB() (*sql.DB, error) {
	return testutil.OpenTestDB()
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// createSavedItem creates an item and sets the saved FLAG on it.
//
// Saved is no longer a status value, so `Status: "saved"` is not a valid way to
// seed a starred item any more — it would store the literal string 'saved' in
// the read-state column, which nothing reads. These fixtures mean "a starred
// item", so they must go through the flag.
func createSavedItem(t *testing.T, queries *store.Queries, params store.CreateItemParams) (store.Item, error) {
	t.Helper()
	if params.Status == "saved" {
		params.Status = "read"
	}
	item, err := queries.CreateItem(t.Context(), params)
	if err != nil {
		return store.Item{}, err
	}
	return queries.SetItemSaved(t.Context(), store.SetItemSavedParams{
		ID: item.ID, Saved: 1, MarkRead: 1,
	})
}
