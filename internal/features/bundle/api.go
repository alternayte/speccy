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

func toAPI(ctx context.Context, q store.Querier, b pgdb.Bundle) (api.Bundle, error) {
	v, err := version.Get(ctx, q, b, nil)
	if err != nil {
		return api.Bundle{}, err
	}
	verdict, runErr, err := review.Summary(ctx, q, b)
	if err != nil {
		return api.Bundle{}, err
	}
	status := "draft" // §9.5: a bundle with no status stream is a draft
	if sv, err := q.GetBundleStatusView(ctx, b.ID); err == nil {
		status = sv.Status
	} else if !errors.Is(err, sql.ErrNoRows) {
		return api.Bundle{}, err
	}
	out := api.Bundle{
		Id: b.ID, Slug: b.Slug, Title: b.Title, ProfileKey: b.ProfileKey, MainDoc: b.MainDoc,
		SourceKind: api.BundleSourceKind(b.SourceKind), CurrentVersion: version.ToAPI(v), UpdatedAt: b.UpdatedAt.UTC(),
		Verdict: verdict, RunError: runErr, Visibility: ptr(api.Visibility(b.Visibility)),
		Status: ptr(api.ReviewStatus(status)),
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
