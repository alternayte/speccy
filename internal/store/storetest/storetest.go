// Package storetest opens a fresh, migrated database for each engine in a test.
// SQLite always runs. Postgres runs when the postgres build tag is set (`just test-pg`).
package storetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alternayte/speccy/internal/store"
)

// Engine opens a fresh, migrated database for one test.
type Engine struct {
	Name string
	Open func(t *testing.T) *store.DB
}

// Engines returns the engines this test binary runs against.
func Engines() []Engine {
	engines := []Engine{{Name: "sqlite", Open: openSQLite}}
	if postgres != nil {
		engines = append(engines, Engine{Name: "postgres", Open: postgres})
	}
	return engines
}

// postgres is set by postgres.go when the postgres build tag is on.
var postgres func(t *testing.T) *store.DB

func openSQLite(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenSQLite(ctx, filepath.Join(t.TempDir(), "speccy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
