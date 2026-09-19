package bundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source/github"
)

func (a *API) sourceAPI(ctx context.Context, src pgdb.GithubSource) (api.GithubSource, error) {
	out := api.GithubSource{Id: src.ID, Repo: src.Repo, Branch: src.Branch, Path: src.Path, HeadCommit: src.HeadCommit, Error: src.Error}
	if src.SyncedAt.Valid {
		t := src.SyncedAt.Time.UTC()
		out.SyncedAt = &t
	}
	bundles, err := a.Service.DB.Queries().ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: a.Service.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return out, err
	}
	for _, b := range bundles {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == src.ID && !b.ArchivedAt.Valid {
			out.Bundles++
		}
	}
	return out, nil
}

// ListGithubSources lists the GitHub sources (REQ-123).
func (a *API) ListGithubSources(ctx context.Context, _ api.ListGithubSourcesRequestObject) (api.ListGithubSourcesResponseObject, error) {
	rows, err := a.Service.DB.Queries().ListGithubSources(ctx, a.Service.Workspace)
	if err != nil {
		return nil, err
	}
	out := api.ListGithubSources200JSONResponse{Items: []api.GithubSource{}}
	for _, r := range rows {
		s, err := a.sourceAPI(ctx, r)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, s)
	}
	return out, nil
}

// AddGithubSource checks that the token can read the repo, adds the source, and syncs it.
func (a *API) AddGithubSource(ctx context.Context, req api.AddGithubSourceRequestObject) (api.AddGithubSourceResponseObject, error) {
	s := a.Service
	c, err := s.github(ctx)
	if err != nil {
		return nil, err
	}
	repo := strings.Trim(strings.TrimSpace(req.Body.Repo), "/")
	repo = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(repo, "https://github.com/"), "github.com/"), ".git")
	if !github.RepoPattern.MatchString(repo) {
		return nil, kernel.Invalid("bad_repo", "Name the repo as owner/name, such as acme/specs.")
	}
	branch := ""
	if req.Body.Branch != nil {
		branch = strings.TrimSpace(*req.Body.Branch)
	}
	def, err := c.Repo(ctx, repo)
	if err != nil {
		return nil, kernel.Invalid("repo_unreadable", "Speccy cannot read %s: %s.", repo, strings.TrimSuffix(err.Error(), "."))
	}
	if branch == "" {
		branch = def
	}
	p := "."
	if req.Body.Path != nil && strings.Trim(*req.Body.Path, "/ ") != "" {
		p = strings.Trim(strings.TrimSpace(*req.Body.Path), "/")
	}
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: repo, Branch: branch, Path: p,
		CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: time.Now().UTC()}
	if err := s.DB.Queries().InsertGithubSource(ctx, src); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, kernel.Conflict("source_exists", "Speccy already reads %s on %s at %s.", repo, branch, p)
		}
		return nil, err
	}
	// A failed first sync keeps the source, with its error, so the admin can fix and retry.
	_ = s.SyncSource(ctx, src.ID, true)
	row, err := s.DB.Queries().GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: src.ID})
	if err != nil {
		return nil, err
	}
	out, err := a.sourceAPI(ctx, row)
	if err != nil {
		return nil, err
	}
	return api.AddGithubSource200JSONResponse(out), nil
}

// DeleteGithubSource archives the source's bundles and removes it.
func (a *API) DeleteGithubSource(ctx context.Context, req api.DeleteGithubSourceRequestObject) (api.DeleteGithubSourceResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	if _, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: req.SourceId}); errors.Is(err, sql.ErrNoRows) {
		return nil, kernel.NotFound("source_not_found", "No GitHub source has this ID.")
	} else if err != nil {
		return nil, err
	}
	bundles, err := q.ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, b := range bundles {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == req.SourceId && !b.ArchivedAt.Valid {
			if err := q.SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID, ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now}); err != nil {
				return nil, err
			}
		}
	}
	if err := q.DeleteGithubSource(ctx, pgdb.DeleteGithubSourceParams{WorkspaceID: s.Workspace, ID: req.SourceId}); err != nil {
		return nil, err
	}
	return api.DeleteGithubSource204Response{}, nil
}

// SyncGithubSource reads the source now.
func (a *API) SyncGithubSource(ctx context.Context, req api.SyncGithubSourceRequestObject) (api.SyncGithubSourceResponseObject, error) {
	s := a.Service
	if err := s.SyncSource(ctx, req.SourceId, true); err != nil {
		if ke, ok := kernel.AsError(err); ok && ke.Status == 404 {
			return nil, err
		}
	}
	row, err := s.DB.Queries().GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: req.SourceId})
	if err != nil {
		return nil, err
	}
	out, err := a.sourceAPI(ctx, row)
	if err != nil {
		return nil, err
	}
	return api.SyncGithubSource200JSONResponse(out), nil
}

// PublishBundle opens a pull request with the bundle's draft (REQ-123).
func (a *API) PublishBundle(ctx context.Context, req api.PublishBundleRequestObject) (api.PublishBundleResponseObject, error) {
	msg := ""
	if req.Body != nil && req.Body.Message != nil {
		msg = *req.Body.Message
	}
	by := kernel.ActorFrom(ctx).Email
	if by == "" {
		by = kernel.ActorFrom(ctx).UserID
	}
	pr, err := a.Service.Publish(ctx, req.BundleId, by, msg)
	if err != nil {
		return nil, err
	}
	return api.PublishBundle200JSONResponse{PrUrl: pr.URL, PrNumber: pr.Number}, nil
}

// DiscardDraft drops a GitHub bundle's draft.
func (a *API) DiscardDraft(ctx context.Context, req api.DiscardDraftRequestObject) (api.DiscardDraftResponseObject, error) {
	v, err := a.Service.DiscardDraft(ctx, req.BundleId, kernel.ActorFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return api.DiscardDraft200JSONResponse{Version: version.ToAPI(v), Changed: true}, nil
}
