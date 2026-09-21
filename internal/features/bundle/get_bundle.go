package bundle

import (
	"context"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
)

// GetBundle returns one bundle.
func (a *API) GetBundle(ctx context.Context, req api.GetBundleRequestObject) (api.GetBundleResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Service.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	out, err := toAPI(ctx, q, b)
	if err != nil {
		return nil, err
	}
	// Only one bundle reads its main doc for this: the list must stay cheap.
	out.Adopt = adoptOf(ctx, q, b, a.Profiles())
	if err := a.full(ctx, q, b, &out); err != nil {
		return nil, err
	}
	return api.GetBundle200JSONResponse(out), nil
}
