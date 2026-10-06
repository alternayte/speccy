package prreview

import (
	"context"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/action"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

// A pending review with no attribution carries no hidden marker. The local state keeps what
// Speccy wrote in it instead, by GitHub node ID (#140), so a later batch still merges into it,
// and list, delete and discard still know Speccy's comments.

// marks returns what the local state keeps of the pending review on a pull request.
func (a *API) marks(ctx context.Context, repo string, pull int) (action.Marks, error) {
	rows, err := a.DB.Queries().ListPrReviewComments(ctx, pgdb.ListPrReviewCommentsParams{WorkspaceID: a.Workspace, Repo: repo, Pull: int64(pull)})
	if err != nil {
		return nil, err
	}
	out := make(action.Marks, len(rows))
	for i, r := range rows {
		out[i] = api.PendingMark{CommentId: r.CommentID, Kind: api.PendingMarkKind(r.Kind), Key: r.Ref, Body: r.Body}
	}
	return out, nil
}

// keep adds marks to the local state. A mark with no comment ID names nothing, so it is left out.
func (a *API) keep(ctx context.Context, repo string, pull int, marks action.Marks) error {
	q := a.DB.Queries()
	for _, m := range marks {
		if m.CommentId == "" {
			continue
		}
		if err := q.InsertPrReviewComment(ctx, pgdb.InsertPrReviewCommentParams{WorkspaceID: a.Workspace, Repo: repo, Pull: int64(pull),
			CommentID: m.CommentId, Kind: string(m.Kind), Ref: m.Key, Body: m.Body, CreatedAt: now()}); err != nil {
			return err
		}
	}
	return nil
}

// unmark removes the marks of one comment that is gone.
func (a *API) unmark(ctx context.Context, repo string, pull int, commentID string) error {
	return a.DB.Queries().DeletePrReviewComment(ctx, pgdb.DeletePrReviewCommentParams{WorkspaceID: a.Workspace, Repo: repo,
		Pull: int64(pull), CommentID: commentID})
}

// forget removes the marks of a pull request. Its pending review is gone: submitted, discarded,
// or about to be replaced by a new one.
func (a *API) forget(ctx context.Context, repo string, pull int) error {
	return a.DB.Queries().DeletePrReviewComments(ctx, pgdb.DeletePrReviewCommentsParams{WorkspaceID: a.Workspace, Repo: repo, Pull: int64(pull)})
}

// keepNew reads the pending review that Speccy just made, and keeps marks for it: what mark
// returns for it.
func (a *API) keepNew(ctx context.Context, gh *github.Client, repo string, pull int, mark func(r *github.PendingReview) action.Marks) error {
	r, err := gh.Pending(ctx, repo, pull)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	return a.keep(ctx, repo, pull, mark(r))
}

// RecordPending is POST /pending-reviews/marks: speccy action --pr --pending posts a pending
// review with its own credential, and keeps its marks here. A review mark starts a new pending
// review, so the marks of an earlier one go.
func (a *API) RecordPending(ctx context.Context, req api.RecordPendingRequestObject) (api.RecordPendingResponseObject, error) {
	if err := a.local(); err != nil {
		return nil, err
	}
	p, err := parsePR(req.Body.Url)
	if err != nil {
		return nil, err
	}
	for _, m := range req.Body.Marks {
		switch m.Kind {
		case api.PendingMarkKindFinding, api.PendingMarkKindAsk, api.PendingMarkKindBody, api.PendingMarkKindReview:
		default:
			return nil, kernel.Invalid("bad_mark", "%q is not a kind of mark.", m.Kind)
		}
		if m.Kind == api.PendingMarkKindReview {
			if err := a.forget(ctx, p.build.Repo, p.build.Pull); err != nil {
				return nil, err
			}
		}
	}
	if err := a.keep(ctx, p.build.Repo, p.build.Pull, req.Body.Marks); err != nil {
		return nil, err
	}
	return api.RecordPending204Response{}, nil
}
