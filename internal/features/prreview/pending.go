package prreview

import (
	"context"
	"slices"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// ListPending is GET /pending-reviews: the comments of the reviewer's pending review.
func (a *API) ListPending(ctx context.Context, req api.ListPendingRequestObject) (api.ListPendingResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	p, err := parsePR(req.Params.Url)
	if err != nil {
		return nil, err
	}
	gh, err := a.client(ctx, p.build)
	if err != nil {
		return nil, err
	}
	r, err := gh.Pending(ctx, p.build.Repo, p.build.Pull)
	if err != nil {
		return nil, github.UnreadableRepo(p.build.Repo, err)
	}
	out := api.PendingReview{Repo: p.build.Repo, Pull: p.build.Pull, Comments: []api.PendingComment{}}
	if r == nil {
		return api.ListPending200JSONResponse(out), nil
	}
	out.Exists, out.ReviewUrl, out.Body = true, &r.URL, &r.Body
	for _, c := range r.Comments {
		pc := api.PendingComment{Id: c.DatabaseID, Path: c.Path, Excerpt: excerpt(c.Body), Speccy: action.BySpeccy(c.Body)}
		if c.Line > 0 {
			line := c.Line
			pc.Line = &line
		}
		out.Comments = append(out.Comments, pc)
	}
	return api.ListPending200JSONResponse(out), nil
}

// DeletePending is POST /pending-reviews/delete: it deletes the named comments of the
// reviewer's pending review.
func (a *API) DeletePending(ctx context.Context, req api.DeletePendingRequestObject) (api.DeletePendingResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	if len(req.Body.Ids) == 0 {
		return nil, kernel.Invalid("no_ids", "Name the comments to delete by their IDs. speccy pending list gives them.")
	}
	p, err := parsePR(req.Body.Url)
	if err != nil {
		return nil, err
	}
	gh, err := a.client(ctx, p.build)
	if err != nil {
		return nil, err
	}
	r, err := gh.Pending(ctx, p.build.Repo, p.build.Pull)
	if err != nil {
		return nil, github.UnreadableRepo(p.build.Repo, err)
	}
	out := api.DeletePending200JSONResponse{Deleted: []int64{}, Missing: []int64{}}
	byID := map[int64]string{}
	if r != nil {
		for _, c := range r.Comments {
			byID[c.DatabaseID] = c.ID
		}
	}
	for _, id := range req.Body.Ids {
		node, ok := byID[id]
		if !ok || slices.Contains(out.Deleted, id) {
			if !ok {
				out.Missing = append(out.Missing, id)
			}
			continue
		}
		if err := gh.DeletePendingComment(ctx, node); err != nil {
			return nil, err
		}
		out.Deleted = append(out.Deleted, id)
	}
	return out, nil
}

// DiscardPending is POST /pending-reviews/discard: it discards the reviewer's pending reviews
// on the named pull requests, the open pull requests of a repo, or the pull requests of a
// batch.
func (a *API) DiscardPending(ctx context.Context, req api.DiscardPendingRequestObject) (api.DiscardPendingResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	urls, repo, batch := deref(req.Body.Urls), deref(req.Body.Repo), req.Body.Batch
	named := 0
	for _, set := range []bool{len(urls) > 0, repo != "", batch != nil} {
		if set {
			named++
		}
	}
	if named != 1 {
		return nil, kernel.Invalid("bad_discard", "Name the pull requests by their URLs, one repo, or one batch.")
	}
	var prs []pr
	switch {
	case len(urls) > 0:
		for _, u := range urls {
			p, err := parsePR(u)
			if err != nil {
				return nil, err
			}
			prs = append(prs, p)
		}
	case repo != "":
		b, err := parseRepo(repo)
		if err != nil {
			return nil, err
		}
		gh, err := a.client(ctx, b)
		if err != nil {
			return nil, err
		}
		pulls, err := gh.OpenPulls(ctx, b.Repo)
		if err != nil {
			return nil, github.UnreadableRepo(b.Repo, err)
		}
		for _, pl := range pulls {
			pb := b
			pb.Pull = pl.Number
			prs = append(prs, pr{build: pb, url: prURL(pb)})
		}
	default:
		if _, err := a.DB.Queries().GetPrBatch(ctx, pgdb.GetPrBatchParams{WorkspaceID: a.Workspace, ID: *batch}); err != nil {
			return nil, notFound(err)
		}
		items, err := a.DB.Queries().ListPrBatchItems(ctx, *batch)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.State != statePosted {
				continue
			}
			p, err := parsePR(it.Url)
			if err != nil {
				return nil, err
			}
			prs = append(prs, p)
		}
	}
	out := api.DiscardPending200JSONResponse{Discarded: []api.PrRef{}, None: []api.PrRef{}}
	for _, p := range prs {
		gh, err := a.client(ctx, p.build)
		if err != nil {
			return nil, err
		}
		ref := api.PrRef{Repo: p.build.Repo, Pull: p.build.Pull, Url: p.url}
		r, err := gh.Pending(ctx, p.build.Repo, p.build.Pull)
		if err != nil {
			return nil, github.UnreadableRepo(p.build.Repo, err)
		}
		if r == nil {
			out.None = append(out.None, ref)
			continue
		}
		if err := gh.DiscardPending(ctx, r.ID); err != nil {
			return nil, err
		}
		out.Discarded = append(out.Discarded, ref)
	}
	return out, nil
}
