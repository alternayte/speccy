// Package db holds the migrations and the sqlc queries for both engines (SDD §11.2).
// The generated code is in db/postgres (package pgdb) and db/sqlite (package sqlitedb).
package db

import "embed"

// Postgres holds the goose migrations for Postgres.
//
//go:embed postgres/migrations/*.sql
var Postgres embed.FS

// SQLite holds the goose migrations for SQLite.
//
//go:embed sqlite/migrations/*.sql
var SQLite embed.FS
