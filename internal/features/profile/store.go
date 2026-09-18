package profile

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
)

// Versioned is a loaded profile and the version the store gave it.
type Versioned struct {
	Loaded
	Version int64
}

// Record stores each profile and returns its version. A profile whose YAML or template
// differs from its latest version gets a new version; old runs keep theirs (REQ-012).
func Record(ctx context.Context, db *store.DB, workspace uuid.UUID, loaded map[string]Loaded, by string) (map[string]Versioned, error) {
	out := map[string]Versioned{}
	err := db.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		now := time.Now().UTC()
		for key, l := range loaded {
			p, err := q.GetProfileByKey(ctx, pgdb.GetProfileByKeyParams{WorkspaceID: workspace, Key: key})
			if errors.Is(err, sql.ErrNoRows) {
				p = pgdb.Profile{ID: kernel.NewID(), WorkspaceID: workspace, Key: key, Name: l.Profile.Name, CurrentVersion: 1}
				if err := q.InsertProfile(ctx, pgdb.InsertProfileParams(p)); err != nil {
					return err
				}
				if err := insertVersion(ctx, q, p.ID, 1, l, by, now); err != nil {
					return err
				}
				out[key] = Versioned{Loaded: l, Version: 1}
				continue
			} else if err != nil {
				return err
			}
			cur, err := q.GetProfileVersion(ctx, pgdb.GetProfileVersionParams{ProfileID: p.ID, Version: p.CurrentVersion})
			if err != nil {
				return err
			}
			if cur.Yaml == string(l.Source) && cur.Template == string(l.TemplateText) {
				out[key] = Versioned{Loaded: l, Version: p.CurrentVersion}
				continue
			}
			next := p.CurrentVersion + 1
			if err := insertVersion(ctx, q, p.ID, next, l, by, now); err != nil {
				return err
			}
			if err := q.SetProfileVersion(ctx, pgdb.SetProfileVersionParams{ID: p.ID, Name: l.Profile.Name, CurrentVersion: next}); err != nil {
				return err
			}
			out[key] = Versioned{Loaded: l, Version: next}
		}
		return nil
	})
	return out, err
}

func insertVersion(ctx context.Context, q store.Querier, id uuid.UUID, v int64, l Loaded, by string, now time.Time) error {
	return q.InsertProfileVersion(ctx, pgdb.InsertProfileVersionParams{
		ProfileID: id, Version: v, Yaml: string(l.Source), Template: string(l.TemplateText),
		Origin: l.Origin, CreatedBy: by, CreatedAt: now,
	})
}
