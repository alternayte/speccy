package bundle

import (
	"context"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the bundle and file endpoints.
type API struct {
	Service *Service
}

func toAPI(ctx context.Context, q store.Querier, b pgdb.Bundle) (api.Bundle, error) {
	v, err := version.Get(ctx, q, b, nil)
	if err != nil {
		return api.Bundle{}, err
	}
	return api.Bundle{
		Id: b.ID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey, MainDoc: b.MainDoc,
		SourceKind: api.BundleSourceKind(b.SourceKind), CurrentVersion: version.ToAPI(v), UpdatedAt: b.UpdatedAt.UTC(),
	}, nil
}
