package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
)

// Workspace returns the workspace of this install, and creates it on first use.
// DEC-020: one workspace per install in 0.1.0.
func (d *DB) Workspace(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.InTx(ctx, func(tx Tx) error {
		q := tx.Queries()
		ws, err := q.GetFirstWorkspace(ctx)
		if err == nil {
			id = ws.ID
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id = kernel.NewID()
		return q.InsertWorkspace(ctx, pgdb.InsertWorkspaceParams{
			ID: id, Name: "Workspace", Settings: dbtype.JSON(`{}`), CreatedAt: time.Now().UTC(),
		})
	})
	return id, err
}
