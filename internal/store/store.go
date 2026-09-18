// Package store opens the database for either engine and applies its migrations.
// DEC-002: SQLite in local mode, Postgres in hosted mode.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/alternayte/speccy/db"
)

// Engine names a database engine.
type Engine string

const (
	SQLite   Engine = "sqlite"
	Postgres Engine = "postgres"
)

// DB is an open database and the engine it runs on. Feature code picks its sqlc queries by Engine.
type DB struct {
	SQL    *sql.DB
	Engine Engine
}

// Tx is a transaction and the engine it runs on.
type Tx struct {
	*sql.Tx
	Engine Engine
}

// OpenSQLite opens the SQLite database file at path, and creates it when it does not exist.
func OpenSQLite(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_time_format", "sqlite")
	sqldb, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// SQLite allows one writer. One connection serialises writes in the process,
	// so a write never fails with SQLITE_BUSY.
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	return &DB{SQL: sqldb, Engine: SQLite}, nil
}

// OpenPostgres opens the Postgres database at dsn.
func OpenPostgres(ctx context.Context, dsn string) (*DB, error) {
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return &DB{SQL: sqldb, Engine: Postgres}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.SQL.Close() }

// Migrations returns the goose provider for this engine's migrations.
func (d *DB) Migrations() (*goose.Provider, error) {
	var (
		dialect goose.Dialect
		fsys    fs.FS
		dir     string
	)
	switch d.Engine {
	case SQLite:
		dialect, fsys, dir = goose.DialectSQLite3, db.SQLite, "sqlite/migrations"
	case Postgres:
		dialect, fsys, dir = goose.DialectPostgres, db.Postgres, "postgres/migrations"
	default:
		return nil, fmt.Errorf("unknown engine %q", d.Engine)
	}
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(dialect, d.SQL, sub)
}

// Migrate applies every pending migration.
func (d *DB) Migrate(ctx context.Context) error {
	p, err := d.Migrations()
	if err != nil {
		return fmt.Errorf("migrate %s: %w", d.Engine, err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate %s: %w", d.Engine, err)
	}
	return nil
}

// InTx runs fn in one transaction. It commits when fn returns nil and rolls back otherwise.
func (d *DB) InTx(ctx context.Context, fn func(Tx) error) error {
	sqltx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(Tx{Tx: sqltx, Engine: d.Engine}); err != nil {
		_ = sqltx.Rollback()
		return err
	}
	return sqltx.Commit()
}
