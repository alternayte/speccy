package bundle

import (
	"context"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// RenameFile renames or moves a file inside the bundle.
func (a *API) RenameFile(ctx context.Context, req api.RenameFileRequestObject) (api.RenameFileResponseObject, error) {
	body := req.Body
	v, changed, err := a.Service.Change(ctx, req.BundleId, body.BaseVersion,
		source.Op{Kind: source.OpRename, Path: body.From, To: body.To}, a.user(ctx), "Renamed "+body.From+" to "+body.To)
	if err != nil {
		return nil, err
	}
	return api.RenameFile200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}
