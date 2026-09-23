package bundle

import (
	"context"
	"io"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
)

// PutFileContent creates or replaces a file: a save in the editor, a new file, or an upload.
func (a *API) PutFileContent(ctx context.Context, req api.PutFileContentRequestObject) (api.PutFileContentResponseObject, error) {
	content, err := io.ReadAll(io.LimitReader(req.Body, source.CeilingFileBytes+1))
	if err != nil {
		return nil, err
	}
	if lim := a.Service.limits(ctx); int64(len(content)) > lim.FileBytes {
		return nil, kernel.TooLarge("file_too_large", "The file is larger than %d MB, which is the limit for one file.", lim.FileBytes>>20)
	}
	v, changed, err := a.Service.Change(ctx, req.DocId, req.Params.BaseVersion,
		source.Op{Kind: source.OpWrite, Path: req.Params.Path, Content: content}, a.user(ctx), "Saved "+req.Params.Path)
	if err != nil {
		return nil, err
	}
	return api.PutFileContent200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}

// user is created_by for changes: the signed-in user, or the local user in local mode.
func (a *API) user(ctx context.Context) string {
	if id := kernel.ActorFrom(ctx).UserID; id != "" {
		return id
	}
	return LocalUser
}
