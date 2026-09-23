package bundle

import (
	"context"
	"database/sql"
	"errors"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/review"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/store"
)

// API serves the bundle and file endpoints.
type API struct {
	Service *Service
	// Profiles returns the current profiles, for bundles made from a template (REQ-016).
	Profiles func() map[string]profile.Versioned
	// Deps are the other features the next action reads (SDD §13.4).
	Deps Deps
}

func toAPI(ctx context.Context, q store.Querier, b pgdb.SpecDoc) (api.SpecDoc, error) {
	v, err := version.Get(ctx, q, b, nil)
	if err != nil {
		return api.SpecDoc{}, err
	}
	verdict, runErr, err := review.Summary(ctx, q, b)
	if err != nil {
		return api.SpecDoc{}, err
	}
	status := "draft" // §9.5: a bundle with no status stream is a draft
	if sv, err := q.GetSpecDocStatusView(ctx, b.ID); err == nil {
		status = sv.Status
	} else if !errors.Is(err, sql.ErrNoRows) {
		return api.SpecDoc{}, err
	}
	out := api.SpecDoc{
		Id: b.ID, BundleId: b.BundleID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey, Path: b.DocPath,
		SourceKind: api.SpecDocSourceKind(b.SourceKind), CurrentVersion: version.ToAPI(v), UpdatedAt: b.UpdatedAt.UTC(),
		Verdict: verdict, RunError: runErr, Status: ptr(api.ReviewStatus(status)),
	}
	if gh, ok := GitHubStateOf(ctx, q, b); ok {
		out.Github = &api.BundleGithub{Repo: gh.Repo, Branch: gh.Branch, Path: gh.Path, Draft: gh.Draft, Ahead: gh.Ahead}
		if gh.PR != "" {
			out.Github.PrUrl = &gh.PR
		}
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }

// bundleToAPI is bundle b with its spec docs. Its state is the worst state of the spec docs.
func bundleToAPI(b pgdb.Bundle, docs []api.SpecDoc) api.Bundle {
	out := api.Bundle{
		Id: b.ID, Slug: b.Slug, Title: b.Title, SourceKind: api.BundleSourceKind(b.SourceKind),
		Visibility: ptr(api.Visibility(b.Visibility)), Docs: docs, UpdatedAt: b.UpdatedAt.UTC(),
		State: api.BundleStateBuildReady,
	}
	for _, d := range docs {
		switch docState(d) {
		case api.BundleStateNotBuildReady:
			out.State = api.BundleStateNotBuildReady
		case api.BundleStateNotReviewed:
			if out.State == api.BundleStateBuildReady {
				out.State = api.BundleStateNotReviewed
			}
		}
		if d.UpdatedAt.After(out.UpdatedAt) {
			out.UpdatedAt = d.UpdatedAt
		}
	}
	if len(docs) == 0 {
		out.State = api.BundleStateNotReviewed
	}
	return out
}

// docState is one spec doc's state for the bundle list: its verdict on the current version.
func docState(d api.SpecDoc) api.BundleState {
	switch {
	case d.Verdict == nil || d.Verdict.VersionNumber != d.CurrentVersion.Number:
		return api.BundleStateNotReviewed
	case d.Verdict.Result == api.VerdictResultBuildReady:
		return api.BundleStateBuildReady
	case d.Verdict.Result == api.VerdictResultNotBuildReady:
		return api.BundleStateNotBuildReady
	}
	return api.BundleStateNotReviewed
}

// specDocs returns the live spec docs of bundle b, each with the next action from the signals
// a list reads.
func (a *API) specDocs(ctx context.Context, q store.Querier, b pgdb.Bundle, waiting []api.Waiver) ([]api.SpecDoc, error) {
	rows, err := q.ListSpecDocsOfBundle(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := []api.SpecDoc{}
	for _, d := range rows {
		ad, err := toAPI(ctx, q, d)
		if err != nil {
			return nil, err
		}
		if err := a.brief(ctx, q, d, &ad, waiting); err != nil {
			return nil, err
		}
		out = append(out, ad)
	}
	return out, nil
}

// waiting returns the waivers that wait for the caller; one read serves every spec doc.
func (a *API) waiting(ctx context.Context) ([]api.Waiver, error) {
	if a.Deps.Waiting == nil {
		return nil, nil
	}
	return a.Deps.Waiting(ctx)
}
