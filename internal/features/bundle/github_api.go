package bundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

func (a *API) sourceAPI(ctx context.Context, src pgdb.GithubSource) (api.GithubSource, error) {
	out := api.GithubSource{Id: src.ID, Repo: src.Repo, Branch: src.Branch, Path: src.Path,
		HeadCommit: src.HeadCommit, Error: src.Error}
	if src.SyncedAt.Valid {
		t := src.SyncedAt.Time.UTC()
		out.SyncedAt = &t
	}
	adopted, err := a.Service.DB.Queries().ListAdoptedTypes(ctx, src.ID)
	if err != nil {
		return out, err
	}
	n := len(adopted)
	out.Adopted = &n
	n, err = a.Service.bundlesOfSource(ctx, src.ID)
	out.Bundles = n
	return out, err
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
		return ref, api.GithubResolved{}, kernel.Invalid("bad_url", "%s", sentence(err.Error()))
	}
	c, err := s.github(ctx, ref.APIURL())
	if err != nil {
		return ref, api.GithubResolved{}, err
	}
	def, err := c.Repo(ctx, ref.Repo)
	if err != nil {
		return ref, api.GithubResolved{}, github.UnreadableRepo(ref.Repo, err)
	}
	if ref, err = c.ResolveBranch(ctx, ref); err != nil {
		return ref, api.GithubResolved{}, github.UnreadableRepo(ref.Repo, err)
	}
	if ref.File && ref.Path == "." {
		return ref, api.GithubResolved{}, kernel.Invalid("bad_url", "%s is the branch %s of %s. Paste the URL of a doc on it, or of a folder.", raw, ref.Branch, ref.Repo)
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
		return ref, out, github.UnreadableRepo(ref.Repo, err)
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

// ResolveGithubUrl says what a source URL names, before the source is made (REQ-128).
func (a *API) ResolveGithubUrl(ctx context.Context, req api.ResolveGithubUrlRequestObject) (api.ResolveGithubUrlResponseObject, error) {
	_, out, err := a.resolve(ctx, req.Body.Url)
	if err != nil {
		return nil, err
	}
	return api.ResolveGithubUrl200JSONResponse(out), nil
}

// AddGithubSource makes a source for the folder of a source URL and syncs it once (REQ-128).
// A URL that names one doc makes a source for the doc's folder, and opens that doc. A URL whose
// folder an existing source covers makes no source. A new source that covers existing ones
// takes their place, with their adopted types and links.
func (a *API) AddGithubSource(ctx context.Context, req api.AddGithubSourceRequestObject) (api.AddGithubSourceResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	ref, res, err := a.resolve(ctx, req.Body.Url)
	if err != nil {
		return nil, err
	}
	folder := ref.Path
	if ref.File {
		folder = path.Dir(ref.Path)
	}
	// A doc that names no type and that no mapping covers needs a profile, which becomes its
	// adopted type.
	adopt := ""
	if ref.File && (res.Profile == nil || (res.Guessed != nil && *res.Guessed)) {
		if req.Body.Profile != nil {
			adopt = strings.TrimSpace(*req.Body.Profile)
		}
		if adopt == "" && res.Profile != nil {
			adopt = *res.Profile
		}
		if adopt == "" {
			return nil, kernel.Invalid("no_profile", "%s names no type, and Speccy cannot guess one. Name a profile: %s.",
				ref.Path, strings.Join(a.profileKeys(), ", "))
		}
		if _, ok := a.Profiles()[adopt]; !ok {
			return nil, kernel.Invalid("no_such_profile", "There is no profile %q. The profiles are: %s.", adopt, strings.Join(a.profileKeys(), ", "))
		}
	}
	all, err := q.ListGithubSources(ctx, s.Workspace)
	if err != nil {
		return nil, err
	}
	var covered []pgdb.GithubSource
	for _, e := range all {
		if e.Repo != ref.Repo || e.Branch != ref.Branch || e.ApiUrl != ref.APIURL() {
			continue
		}
		if github.Under(folder, e.Path) {
			if adopt != "" {
				if err := q.SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: e.ID, Path: ref.Path, Profile: adopt}); err != nil {
					return nil, err
				}
				_ = s.SyncSource(ctx, e.ID, true)
			}
			return a.added(ctx, e, ref, true)
		}
		if github.Under(e.Path, folder) {
			covered = append(covered, e)
		}
	}
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: ref.Repo, Branch: ref.Branch,
		Path: folder, ApiUrl: ref.APIURL(), CreatedBy: kernel.ActorFrom(ctx).UserID, CreatedAt: time.Now().UTC()}
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if err := q.InsertGithubSource(ctx, src); err != nil {
			return err
		}
		for _, e := range covered {
			if err := q.MoveAdoptedTypes(ctx, pgdb.MoveAdoptedTypesParams{ToSource: src.ID, FromSource: e.ID}); err != nil {
				return err
			}
			if err := q.MoveAdoptedLinks(ctx, pgdb.MoveAdoptedLinksParams{ToSource: src.ID, FromSource: e.ID}); err != nil {
				return err
			}
			// The new source takes over the spec docs of a source that is gone.
			if err := q.DeleteGithubSource(ctx, pgdb.DeleteGithubSourceParams{WorkspaceID: s.Workspace, ID: e.ID}); err != nil {
				return err
			}
		}
		if adopt != "" {
			return q.SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: src.ID, Path: ref.Path, Profile: adopt})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A failed first sync keeps the source, with its error, so a person can fix and retry. The
	// response carries the error, and the dialog shows it (#67).
	_ = s.SyncSource(ctx, src.ID, true)
	row, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: src.ID})
	if err != nil {
		return nil, err
	}
	return a.added(ctx, row, ref, false)
}

// added is the response of AddGithubSource: the source, and the bundle and the spec doc the
// URL names, when the source holds them.
func (a *API) added(ctx context.Context, src pgdb.GithubSource, ref github.Ref, already bool) (api.AddGithubSourceResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	out, err := a.sourceAPI(ctx, src)
	if err != nil {
		return nil, err
	}
	res := api.AddedSource{Source: out, AlreadyAdded: already}
	docs, err := q.ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		var r githubRef
		_ = json.Unmarshal(d.SourceRef, &r)
		if r.Source != src.ID || d.ArchivedAt.Valid {
			continue
		}
		if ref.File && path.Join(r.Dir, d.DocPath) == ref.Path {
			res.BundleId, res.DocId = &d.BundleID, &d.ID
			break
		}
		if !ref.File && res.BundleId == nil && github.Under(r.Dir, ref.Path) {
			res.BundleId = &d.BundleID
		}
	}
	return api.AddGithubSource200JSONResponse(res), nil
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
	bundles, err := q.ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, b := range bundles {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == req.SourceId && !b.ArchivedAt.Valid {
			if err := q.SetSpecDocArchived(ctx, pgdb.SetSpecDocArchivedParams{ID: b.ID, ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now}); err != nil {
				return nil, err
			}
		}
	}
	if err := q.DeleteGithubSource(ctx, pgdb.DeleteGithubSourceParams{WorkspaceID: s.Workspace, ID: req.SourceId}); err != nil {
		return nil, err
	}
	if err := s.archiveEmptyBundles(ctx, q, KindGitHub, nil, now); err != nil {
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
	pr, err := a.Service.Publish(ctx, req.DocId, by, msg)
	if err != nil {
		return nil, err
	}
	return api.PublishBundle200JSONResponse{PrUrl: pr.URL, PrNumber: pr.Number}, nil
}

// DiscardDraft drops a GitHub bundle's draft.
func (a *API) DiscardDraft(ctx context.Context, req api.DiscardDraftRequestObject) (api.DiscardDraftResponseObject, error) {
	v, err := a.Service.DiscardDraft(ctx, req.DocId, kernel.ActorFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return api.DiscardDraft200JSONResponse{Version: version.ToAPI(v), Changed: true}, nil
}

// skippedLimit is how many skipped docs the list holds. Above it, the source covers too much
// of the repo, and the answer says so instead of holding a 2000-row screen.
const skippedLimit = 200

// ListSkippedDocs lists the markdown files under the source that the scan passed over, with a
// guessed doc type each (REQ-133).
func (a *API) ListSkippedDocs(ctx context.Context, req api.ListSkippedDocsRequestObject) (api.ListSkippedDocsResponseObject, error) {
	src, err := a.Service.DB.Queries().GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: a.Service.Workspace, ID: req.SourceId})
	if err != nil {
		return nil, err
	}
	var all []string
	_ = json.Unmarshal(src.Skipped, &all)
	gone, err := a.dismissed(ctx)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range all {
		if !gone[src.ID][p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	total := len(paths)
	if len(paths) > skippedLimit {
		paths = paths[:skippedLimit]
	}
	adopted, err := a.Service.DB.Queries().ListAdoptedTypes(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	was := map[string]string{}
	for _, t := range adopted {
		was[t.Path] = t.Profile
	}
	c, err := a.Service.GitHub(ctx, src.ApiUrl)
	if err != nil {
		return nil, err
	}
	out := api.ListSkippedDocs200JSONResponse{Items: []api.SourceSkippedDoc{}, Total: total}
	for _, p := range paths {
		item := api.SourceSkippedDoc{Path: p, Adopted: was[p]}
		if content, ok, err := c.FileAt(ctx, src.Repo, src.Branch, p); err == nil && ok {
			if key, sure := profile.Guess(a.Profiles(), content); sure {
				item.Guess = &key
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// AdoptSkippedDocs stores the doc type a person accepted for skipped docs, and syncs, so the
// bundles appear. The repo takes no commit (REQ-133).
func (a *API) AdoptSkippedDocs(ctx context.Context, req api.AdoptSkippedDocsRequestObject) (api.AdoptSkippedDocsResponseObject, error) {
	s := a.Service
	src, err := s.DB.Queries().GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: req.SourceId})
	if err != nil {
		return nil, err
	}
	var paths []string
	_ = json.Unmarshal(src.Skipped, &paths)
	known := map[string]bool{}
	for _, p := range paths {
		known[p] = true
	}
	// Every item is checked before one is stored, so a bad item changes nothing (#73).
	for _, it := range req.Body.Items {
		if _, ok := a.Profiles()[it.Profile]; !ok {
			return nil, kernel.Invalid("no_profile", "There is no doc type %q.", it.Profile)
		}
		if !known[it.Path] {
			return nil, kernel.Invalid("not_skipped", "%s is not a file the scan passed over.", it.Path)
		}
		if l := it.Link; l != nil && !checkLinkKind(l.Kind) {
			return nil, kernel.Invalid("bad_link", "There is no link kind %q.", l.Kind)
		}
	}
	for _, it := range req.Body.Items {
		if err := s.DB.Queries().SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: src.ID, Path: it.Path, Profile: it.Profile}); err != nil {
			return nil, err
		}
		// A confirmed link stays in Speccy, as the type does: the repo takes no commit.
		if l := it.Link; l != nil {
			if err := s.DB.Queries().SetAdoptedLink(ctx, pgdb.SetAdoptedLinkParams{SourceID: src.ID, Path: it.Path,
				Kind: l.Kind, Target: path.Clean(l.Target)}); err != nil {
				return nil, err
			}
		}
		// Accepting a type contradicts the mark, so the newer act wins (REQ-133).
		if err := s.DB.Queries().DeleteDismissedDoc(ctx, pgdb.DeleteDismissedDocParams{WorkspaceID: s.Workspace,
			SourceID: uuid.NullUUID{UUID: src.ID, Valid: true}, Path: it.Path}); err != nil {
			return nil, err
		}
	}
	// A sync that fails keeps its reason on the source, as it does when the source is added
	// and when a person asks for a sync. The accepted types stand, so a person can fix the
	// cause and try again.
	if err := s.SyncSource(ctx, src.ID, true); err != nil {
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
	return api.AdoptSkippedDocs200JSONResponse(out), nil
}

// PublishSourceMapping opens a pull request that writes the accepted types into the repo's
// .speccy.yaml, so the Action reads the same answer as the app (REQ-133).
func (a *API) PublishSourceMapping(ctx context.Context, req api.PublishSourceMappingRequestObject) (api.PublishSourceMappingResponseObject, error) {
	by := kernel.ActorFrom(ctx).Email
	if by == "" {
		by = kernel.ActorFrom(ctx).UserID
	}
	pr, err := a.Service.PublishMapping(ctx, req.SourceId, by)
	if err != nil {
		return nil, err
	}
	return api.PublishSourceMapping200JSONResponse{PrUrl: pr.URL, PrNumber: pr.Number}, nil
}
