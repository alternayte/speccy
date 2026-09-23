package bundle

import (
	"context"
	"database/sql"
	"errors"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// GetBundle returns one bundle with its spec docs.
func (a *API) GetBundle(ctx context.Context, req api.GetBundleRequestObject) (api.GetBundleResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := q.GetBundle(ctx, pgdb.GetBundleParams{WorkspaceID: a.Service.Workspace, ID: req.BundleId})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b.ArchivedAt.Valid) {
		return nil, kernel.NotFound("bundle_not_found", "No bundle has the ID %s.", req.BundleId)
	}
	if err != nil {
		return nil, err
	}
	waiting, err := a.waiting(ctx)
	if err != nil {
		return nil, err
	}
	docs, err := a.specDocs(ctx, q, b, waiting)
	if err != nil {
		return nil, err
	}
	return api.GetBundle200JSONResponse(bundleToAPI(b, docs)), nil
}

// GetSpecDoc returns one spec doc.
func (a *API) GetSpecDoc(ctx context.Context, req api.GetSpecDocRequestObject) (api.GetSpecDocResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Service.Workspace, req.DocId)
	if err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, q, b)
	if err != nil {
		return nil, err
	}
	// Only one spec doc reads its text for this: the list must stay cheap.
	out.Adopt = adoptOf(ctx, q, b, a.Profiles())
	if err := a.full(ctx, q, b, &out); err != nil {
		return nil, err
	}
	return api.GetSpecDoc200JSONResponse(out), nil
}
