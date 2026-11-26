package bundle

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/share"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/store"
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
	closed, err := a.closedBundles(ctx, q)
	if err != nil {
		return nil, err
	}
	withClosed := req.Params.IncludeClosed != nil && *req.Params.IncludeClosed
	hidden := 0
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
			if !ok {
				continue
			}
			if _, gone := closed[b.ID]; gone && !withClosed {
				hidden++
				continue
			}
			rows = append(rows, b)
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
		item := bundleToAPI(b, docs)
		if c, ok := closed[b.ID]; ok {
			item.Closed = &c
		}
		out.Items = append(out.Items, item)
	}
	if !withClosed {
		out.Closed = &hidden
	}
	for _, p := range s.Problems() {
		out.Problems = append(out.Problems, api.BundleProblem{Path: p.Path, Message: p.Message})
	}
	return api.ListBundles200JSONResponse(out), nil
}

// closedBundles names each bundle whose every spec doc is read-only, because its pull request
// closed or because the doc moved to a bundle on the base branch
// (docs/specs/self-serve-pull-request-source.md). merged means every doc moved.
func (a *API) closedBundles(ctx context.Context, q store.Querier) (map[uuid.UUID]api.BundleClosed, error) {
	docs, err := q.ListSpecDocsOfBundles(ctx, a.Service.Workspace)
	if err != nil {
		return nil, err
	}
	type tally struct{ docs, closed, merged int }
	by := map[uuid.UUID]*tally{}
	for _, d := range docs {
		t := by[d.BundleID]
		if t == nil {
			t = &tally{}
			by[d.BundleID] = t
		}
		t.docs++
		if d.SourceKind != KindGitHub {
			continue
		}
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		switch ref.ReadOnly {
		case ReadOnlyMerged:
			t.merged++
		case "":
		default:
			t.closed++
		}
	}
	out := map[uuid.UUID]api.BundleClosed{}
	for id, t := range by {
		switch {
		case t.merged == t.docs:
			out[id] = api.BundleClosedMerged
		case t.closed+t.merged == t.docs:
			out[id] = api.BundleClosedClosed
		}
	}
	return out, nil
}
