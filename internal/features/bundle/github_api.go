package bundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

func (a *API) sourceAPI(ctx context.Context, src pgdb.GithubSource) (api.GithubSource, error) {
	out := api.GithubSource{Id: src.ID, Repo: src.Repo, Branch: src.Branch, Path: src.Path, File: src.IsFile,
		HeadCommit: src.HeadCommit, Error: src.Error}
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

// resolve reads a source URL and asks GitHub what it names: the branch, the folder or the
// doc, and the profile the doc would use (REQ-128). It makes no source.
func (a *API) resolve(ctx context.Context, raw string) (github.Ref, api.GithubResolved, error) {
	s := a.Service
	ref, err := github.ParseURL(raw)
	if err != nil {
		return ref, api.GithubResolved{}, kernel.Invalid("bad_url", "%s.", sentence(err.Error()))
	}
	c, err := s.github(ctx, ref.APIURL())
	if err != nil {
		return ref, api.GithubResolved{}, err
	}
	def, err := c.Repo(ctx, ref.Repo)
	if err != nil {
		return ref, api.GithubResolved{}, repoUnreadable(ref.Repo, err)
	}
	if ref.Branch == "" {
		ref.Branch = def
	}
	out := api.GithubResolved{Repo: ref.Repo, Branch: ref.Branch, Path: ref.Path, File: ref.File, Profiles: a.profileKeys()}
	if !ref.File {
		return ref, out, nil
	}
	// A one-doc URL: the doc says its type, or the repo's .speccy.yaml maps it, or Speccy
	// guesses and the person confirms (REQ-128).
	content, ok, err := c.FileAt(ctx, ref.Repo, ref.Branch, ref.Path)
	if err != nil {
		return ref, out, repoUnreadable(ref.Repo, err)
	}
	if !ok {
		return ref, out, kernel.NotFound("doc_not_found", "%s is not on %s of %s.", ref.Path, ref.Branch, ref.Repo)
	}
	if !source.IsMarkdown(ref.Path) {
		return ref, out, kernel.Invalid("not_markdown", "%s is not a markdown file. A bundle's main doc is markdown.", ref.Path)
	}
	fm, _, _ := source.ReadFrontmatter(content)
	key, guessed := fm.Type, false
	if key == "" {
		if cfg, err := a.repoConfig(ctx, c, ref); err == nil {
			key, _ = cfg.MappedProfile(ref.Path)
		}
	}
	if key == "" {
		key, _ = profile.Guess(a.Profiles(), content)
		guessed = true
	}
	if key != "" {
		out.Profile, out.Guessed = &key, &guessed
	}
	if t := docTitle(content); t != "" {
		out.Title = &t
	}
	return ref, out, nil
}

// repoConfig reads the .speccy.yaml of the ref's branch. A repo with none maps nothing.
func (a *API) repoConfig(ctx context.Context, c *github.Client, ref github.Ref) (source.RepoConfig, error) {
	raw, ok, err := c.FileAt(ctx, ref.Repo, ref.Branch, source.RepoConfigFile)
	if err != nil || !ok {
		return source.RepoConfig{}, err
	}
	return source.ParseRepoConfig(raw)
}

func (a *API) profileKeys() []string {
	out := make([]string, 0, len(a.Profiles()))
	for k := range a.Profiles() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// docTitle is the first heading of a doc, for the dialog.
func docTitle(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// repoUnreadable says that the credentials cannot read the repo, which GitHub answers with a
// 404 for a private repo (REQ-129).
func repoUnreadable(repo string, err error) error {
	if github.IsNotFound(err) {
		return kernel.Invalid("repo_no_access", "The GitHub credentials have no access to %s. It may be private, or the token may not cover it.", repo)
	}
	return kernel.Invalid("repo_unreadable", "Speccy cannot read %s: %s.", repo, strings.TrimSuffix(err.Error(), "."))
}

// ResolveGithubUrl says what a source URL names, before the source is made (REQ-128).
func (a *API) ResolveGithubUrl(ctx context.Context, req api.ResolveGithubUrlRequestObject) (api.ResolveGithubUrlResponseObject, error) {
	_, out, err := a.resolve(ctx, req.Body.Url)
	if err != nil {
		return nil, err
	}
	return api.ResolveGithubUrl200JSONResponse(out), nil
}

// AddGithubSource makes a source from a source URL and syncs it once (REQ-128).
func (a *API) AddGithubSource(ctx context.Context, req api.AddGithubSourceRequestObject) (api.AddGithubSourceResponseObject, error) {
	s := a.Service
	ref, res, err := a.resolve(ctx, req.Body.Url)
	if err != nil {
		return nil, err
	}
	key := ""
	if req.Body.Profile != nil {
		key = strings.TrimSpace(*req.Body.Profile)
	}
	if key == "" && res.Profile != nil {
		key = *res.Profile
	}
	if ref.File {
		if key == "" {
			return nil, kernel.Invalid("no_profile", "%s names no type, and Speccy cannot guess one. Name a profile: %s.",
				ref.Path, strings.Join(a.profileKeys(), ", "))
		}
		if _, ok := a.Profiles()[key]; !ok {
			return nil, kernel.Invalid("no_such_profile", "There is no profile %q. The profiles are: %s.", key, strings.Join(a.profileKeys(), ", "))
		}
	} else {
		key = ""
	}
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: ref.Repo, Branch: ref.Branch,
		Path: ref.Path, IsFile: ref.File, Profile: key, ApiUrl: ref.APIURL(),
		CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: time.Now().UTC()}
	if err := s.DB.Queries().InsertGithubSource(ctx, src); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, kernel.Conflict("source_exists", "Speccy already reads %s on %s at %s.", ref.Repo, ref.Branch, ref.Path)
		}
		return nil, err
	}
	// A failed first sync keeps the source, with its error, so a person can fix and retry.
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
