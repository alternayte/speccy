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
	content, err := io.ReadAll(io.LimitReader(req.Body, source.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > source.MaxFileBytes {
		return nil, kernel.TooLarge("file_too_large", "The file is larger than 10 MB, which is the limit for one file.")
	}
	v, changed, err := a.Service.Change(ctx, req.BundleId, req.Params.BaseVersion,
		source.Op{Kind: source.OpWrite, Path: req.Params.Path, Content: content}, a.user(), "Saved "+req.Params.Path)
	if err != nil {
		return nil, err
	}
	return api.PutFileContent200JSONResponse{Version: version.ToAPI(v), Changed: changed}, nil
}

// user is created_by for changes. Local mode has one implicit user; M8 adds accounts.
func (a *API) user() string { return LocalUser }
