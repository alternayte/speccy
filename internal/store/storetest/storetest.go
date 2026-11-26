// Package storetest opens a fresh, migrated database for each engine in a test.
// SQLite always runs. Postgres runs when the postgres build tag is set (`just test-pg`).
package storetest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alternayte/speccy/internal/queue"
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

// The migrations take most of the time of a small test, so each test binary migrates one
// SQLite file, Speccy's tables and River's, and each test gets a copy of it.
var sqliteTemplate = sync.OnceValues(func() ([]byte, error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "speccy-storetest-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "speccy.db")
	db, err := store.OpenSQLite(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := queue.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Closing the last connection moves the WAL into the file, so the file alone holds it all.
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func openSQLite(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	tmpl, err := sqliteTemplate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "speccy.db")
	if err := os.WriteFile(path, tmpl, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
