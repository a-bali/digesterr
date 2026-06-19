package store

import (
	"encoding/json"
	"database/sql"
)

func ptrFromNullInt64(n sql.NullInt64) *int64 {
	if n.Valid {
		return &n.Int64
	}
	return nil
}

func (f Feed) MarshalJSON() ([]byte, error) {
	type Alias Feed
	return json.Marshal(&struct {
		CategoryID *int64 `json:"category_id"`
		*Alias
	}{
		CategoryID: ptrFromNullInt64(f.CategoryID),
		Alias:      (*Alias)(&f),
	})
}
