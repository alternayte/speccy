package bundle

import (
	"context"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// localSource stands for "no source": the file is in the served folder. The unique index of
// dismissed_doc reads a null source_id as this ID, so one table holds both kinds.
var localSource = uuid.UUID{}

// dismissed returns the paths a person marked as not a spec, by source. The key of a file in
// the served folder is localSource.
func (a *API) dismissed(ctx context.Context) (map[uuid.UUID]map[string]bool, error) {
	rows, err := a.Service.DB.Queries().ListDismissedDocs(ctx, a.Service.Workspace)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]map[string]bool{}
	for _, r := range rows {
		key := localSource
		if r.SourceID.Valid {
			key = r.SourceID.UUID
		}
		if out[key] == nil {
			out[key] = map[string]bool{}
		}
		out[key][r.Path] = true
	}
	return out, nil
}

// ListDismissedDocs lists the files a person marked as not a spec (REQ-133).
func (a *API) ListDismissedDocs(ctx context.Context, _ api.ListDismissedDocsRequestObject) (api.ListDismissedDocsResponseObject, error) {
	rows, err := a.Service.DB.Queries().ListDismissedDocs(ctx, a.Service.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.ListDismissedDocs200JSONResponse{Items: []api.DismissedDoc{}}
	for _, r := range rows {
		item := api.DismissedDoc{Path: r.Path}
		if r.SourceID.Valid {
			id := r.SourceID.UUID
			item.SourceId = &id
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// DismissDoc marks a file as not a spec. Speccy writes nothing to the repo or the file.
func (a *API) DismissDoc(ctx context.Context, req api.DismissDocRequestObject) (api.DismissDocResponseObject, error) {
	if req.Body.Path == "" {
		return nil, kernel.Invalid("no_path", "Name the file to mark.")
	}
	src := uuid.NullUUID{}
	if req.Body.SourceId != nil {
		src = uuid.NullUUID{UUID: *req.Body.SourceId, Valid: true}
	}
	by := kernel.ActorFrom(ctx).UserID
	if err := a.Service.DB.Queries().InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{
		WorkspaceID: a.Service.Workspace, SourceID: src, Path: req.Body.Path, DismissedBy: by, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, err
	}
	return api.DismissDoc204Response{}, nil
}

// UndismissDoc takes the mark off a file, so it appears again.
func (a *API) UndismissDoc(ctx context.Context, req api.UndismissDocRequestObject) (api.UndismissDocResponseObject, error) {
	src := uuid.NullUUID{}
	if req.Params.SourceId != nil {
		src = uuid.NullUUID{UUID: *req.Params.SourceId, Valid: true}
	}
	if err := a.Service.DB.Queries().DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{
		WorkspaceID: a.Service.Workspace, SourceID: src, Path: req.Params.Path,
	}); err != nil {
		return nil, err
	}
	return api.UndismissDoc204Response{}, nil
}
