package bundle

import (
	"context"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// DeleteFile deletes a file from the bundle.
func (a *API) DeleteFile(ctx context.Context, req api.DeleteFileRequestObject) (api.DeleteFileResponseObject, error) {
	v, changed, err := a.Service.Change(ctx, req.DocId, req.Params.BaseVersion,
		source.Op{Kind: source.OpDelete, Path: req.Params.Path}, a.user(ctx), "Deleted "+req.Params.Path)
	if err != nil {
		return nil, err
	}
	return api.DeleteFile200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}
