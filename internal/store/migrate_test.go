package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A state that a newer Speccy migrated stops at start with a message that names both
// migration versions, not with a failed query later.
func TestMigrate_StateFromNewerSpeccy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenSQLite(ctx, filepath.Join(dir, "speccy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	sources := p.ListSources()
	newest := sources[len(sources)-1].Version
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, newest+1); err != nil {
		t.Fatal(err)
	}
	err = db.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprint(newest+1)) || !strings.Contains(err.Error(), fmt.Sprint(newest)) ||
		!strings.Contains(err.Error(), "newer Speccy") {
		t.Fatalf("Migrate = %v, want the message that names migrations %d and %d and says to run the newer Speccy", err, newest+1, newest)
	}
}

// Two processes on one state folder each read, then write, in a transaction (#86). The second
// writer waits for the first; it does not fail with "database is locked". Two handles stand in
// for the two processes.
func TestInTx_TwoProcessesReadThenWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "speccy.db")
	a, b := openSQLite(t, path), openSQLite(t, path)
	if _, err := a.SQL.ExecContext(ctx, `CREATE TABLE t (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	readThenWrite := func(db *store.DB, v string, read chan<- struct{}, wait <-chan struct{}) error {
		return db.InTx(ctx, func(tx store.Tx) error {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
				return err
			}
			if read != nil {
				close(read)
				<-wait
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO t (v) VALUES (?)`, v)
			return err
		})
	}
	read, wait, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- readThenWrite(a, "a", read, wait) }()
	<-read
	// b starts while a holds its transaction open, and a writes after b asked for the lock.
	other := make(chan error, 1)
	go func() { other <- readThenWrite(b, "b", nil, nil) }()
	time.Sleep(200 * time.Millisecond)
	close(wait)
	if err := <-done; err != nil {
		t.Errorf("the first transaction: %v", err)
	}
	if err := <-other; err != nil {
		t.Errorf("the second transaction: %v", err)
	}
}

// Several processes that start on a new state folder at the same time each apply the
// migrations (#86). One applies them, and the others find them applied.
func TestMigrate_ProcessesStartTogether(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "speccy.db")
	errs := make(chan error, 6)
	for range cap(errs) {
		go func() {
			db, err := store.OpenSQLite(ctx, path)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = db.Close() }()
			errs <- db.Migrate(ctx)
		}()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func openSQLite(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
