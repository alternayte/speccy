package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alternayte/speccy/internal/store"
)

// A state from before 0.15.0 stops at start with a message that says what to do, not with a
// failed migration.
func TestMigrate_StateBeforeBaseline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenSQLite(ctx, filepath.Join(dir, "speccy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Goose's version table as a 0.14 state left it.
	if _, err := db.SQL.ExecContext(ctx, `CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')));
		INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (29, 1)`); err != nil {
		t.Fatal(err)
	}
	err = db.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "before 0.15.0") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Migrate = %v, want the message about a state from before 0.15.0 in %s", err, dir)
	}
}
