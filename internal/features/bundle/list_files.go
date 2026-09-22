package bundle

import (
	"context"
	"strings"

	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
)

// ListFiles returns the files of a version, sorted by path, and marks the main doc.
func (a *API) ListFiles(ctx context.Context, req api.ListFilesRequestObject) (api.ListFilesResponseObject, error) {
	q := a.Service.DB.Queries()
	b, err := version.Bundle(ctx, q, a.Service.Workspace, req.BundleId)
	if err != nil {
		return nil, err
	}
	v, err := version.Get(ctx, q, b, req.Params.Version)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListVersionFiles(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	main := b.MainDoc
	if v.ID != b.CurrentVersionID.UUID {
		// An older version can have another main doc. Read only its top-level markdown files.
		var candidates []source.File
		for _, r := range rows {
			if !strings.Contains(r.Path, "/") && source.IsMarkdown(r.Path) {
				content, err := q.GetBlob(ctx, r.Sha256)
				if err != nil {
					return nil, err
				}
				candidates = append(candidates, source.File{Path: r.Path, Content: content})
			}
		}
		main = ""
		if m, err := source.FindMainDoc(candidates); err == nil {
			main = m.Path
		}
	}
	out := api.FileList{Version: version.ToAPI(v), Items: make([]api.BundleFile, len(rows))}
	for i, r := range rows {
		out.Items[i] = api.BundleFile{Path: r.Path, Size: r.Size, Sha256: r.Sha256, IsMainDoc: r.Path == main}
		if r.CarriedBy != "" {
			by := r.CarriedBy
			out.Items[i].CarriedBy = &by
		}
	}
	return api.ListFiles200JSONResponse(out), nil
}
