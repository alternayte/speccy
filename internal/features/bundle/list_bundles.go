package bundle

import (
	"context"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/http/api"
)

// ListBundles returns one page of bundles sorted by slug, and the folders that are not valid
// bundles (REQ-001). The cursor is the last slug of the previous page.
func (a *API) ListBundles(ctx context.Context, req api.ListBundlesRequestObject) (api.ListBundlesResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	limit := int64(50)
	if req.Params.Limit != nil {
		limit = int64(*req.Params.Limit)
	}
	after := ""
	if req.Params.Cursor != nil {
		after = *req.Params.Cursor
	}
	rows, err := q.ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: s.Workspace, AfterSlug: after, PageSize: limit + 1})
	if err != nil {
		return nil, err
	}
	out := api.BundleList{Items: []api.Bundle{}, Problems: []api.BundleProblem{}}
	for i, b := range rows {
		if int64(i) == limit {
			next := rows[i-1].Slug
			out.NextCursor = &next
			break
		}
		ab, err := toAPI(ctx, q, b)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, ab)
	}
	for _, p := range s.Problems() {
		out.Problems = append(out.Problems, api.BundleProblem{Path: p.Path, Message: p.Message})
	}
	return api.ListBundles200JSONResponse(out), nil
}
