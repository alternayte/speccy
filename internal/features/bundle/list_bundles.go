package bundle

import (
	"context"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
)

// ListBundles returns one page of bundles sorted by slug, each with its spec docs, and the
// folders that are not valid bundles (REQ-001). The cursor is the last slug of the previous page.
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
	// REQ-084: the list holds only the bundles the actor can see. Hidden bundles are skipped,
	// so pages are read until this page is full.
	actor := kernel.ActorFrom(ctx)
	var rows []pgdb.Bundle
	for int64(len(rows)) <= limit {
		page, err := q.ListBundles(ctx, pgdb.ListBundlesParams{WorkspaceID: s.Workspace, AfterSlug: after, PageSize: limit + 1})
		if err != nil {
			return nil, err
		}
		for _, b := range page {
			ok, err := share.CanReadBundle(ctx, q, actor, b)
			if err != nil {
				return nil, err
			}
			if ok {
				rows = append(rows, b)
			}
		}
		if int64(len(page)) < limit+1 {
			break
		}
		after = page[len(page)-1].Slug
	}
	waiting, err := a.waiting(ctx)
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
		docs, err := a.specDocs(ctx, q, b, waiting)
		if err != nil {
			return nil, err
		}
		if len(docs) == 0 {
			continue
		}
		out.Items = append(out.Items, bundleToAPI(b, docs))
	}
	for _, p := range s.Problems() {
		out.Problems = append(out.Problems, api.BundleProblem{Path: p.Path, Message: p.Message})
	}
	return api.ListBundles200JSONResponse(out), nil
}
