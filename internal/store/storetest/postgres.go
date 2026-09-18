//go:build postgres

package storetest

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/alternayte/speccy/internal/store"
)

// One container serves the whole test binary. Each test gets its own database.
var (
	pgOnce sync.Once
	pgDSN  string
	pgErr  error
	pgSeq  atomic.Int64
)

func init() { postgres = openPostgres }

func startPostgres() {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("speccy"),
		tcpostgres.WithUsername("speccy"),
		tcpostgres.WithPassword("speccy"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	// The Ryuk reaper removes the container when the test binary exits.
	pgDSN, pgErr = c.ConnectionString(ctx, "sslmode=disable")
}

func openPostgres(t *testing.T) *store.DB {
	t.Helper()
	pgOnce.Do(startPostgres)
	if pgErr != nil {
		t.Fatal(pgErr)
	}
	ctx := context.Background()
	admin, err := store.OpenPostgres(ctx, pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	name := fmt.Sprintf("t%d_%s", pgSeq.Add(1), strings.ToLower(strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name())))
	if len(name) > 63 {
		name = name[:63]
	}
	if _, err := admin.SQL.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := store.OpenPostgres(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
