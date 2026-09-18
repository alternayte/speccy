// Package dbtype holds column types that both engines share.
package dbtype

import (
	"database/sql/driver"
	"fmt"
)

// JSON is a JSON column: jsonb in Postgres, JSON text in SQLite. It scans text or bytes and
// writes text, so SQLite stores TEXT (not BLOB) and its JSON functions and defaults work.
type JSON []byte

// Scan implements sql.Scanner.
func (j *JSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case string:
		*j = JSON(v)
	case []byte:
		*j = append(JSON(nil), v...)
	default:
		return fmt.Errorf("dbtype.JSON: cannot scan %T", src)
	}
	return nil
}

// Value implements driver.Valuer.
func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return "null", nil
	}
	return string(j), nil
}

// MarshalJSON returns the JSON as is.
func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}
