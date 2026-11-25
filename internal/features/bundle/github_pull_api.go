package bundle

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// resolvedDoc is one markdown file of a pull request, and whether its type is its own.
type resolvedDoc struct {
	api.ResolvedDoc
	own bool // the doc names its type, or the repo's .speccy.yaml maps it
}

// resolvePull reads a pull request URL: the pull request, and the markdown files it changes at
// its head, each with the type it names, the mapping gives, or Speccy guesses.
func (a *API) resolvePull(ctx context.Context, c *github.Client, ref github.Ref) (api.GithubResolved, []resolvedDoc, error) {
	info, err := c.PullRequest(ctx, ref.Repo, ref.Pull)
	if err != nil {
		return api.GithubResolved{}, nil, github.UnreadableRepo(ref.Repo, err)
	}
	files, err := c.PRFiles(ctx, ref.Repo, ref.Pull)
	if err != nil {
		return api.GithubResolved{}, nil, github.UnreadableRepo(ref.Repo, err)
	}
	state := info.State
	out := api.GithubResolved{Repo: ref.Repo, Branch: info.HeadRef, Path: ".", Profiles: a.profileKeys(),
		Pull: &api.PullInfo{Number: ref.Pull, Title: info.Title, Url: info.URL, Author: info.Author, State: api.PullInfoState(state),
			HeadBranch: info.HeadRef, BaseBranch: info.BaseRef, Draft: &info.Draft}}
	cfg := source.RepoConfig{}
	if raw, ok, err := c.FileAt(ctx, ref.Repo, info.HeadSHA, source.RepoConfigFile); err == nil && ok {
		cfg, _ = source.ParseRepoConfig(raw)
	}
	var docs []resolvedDoc
	for _, p := range changedMarkdown(files) {
		d := resolvedDoc{ResolvedDoc: api.ResolvedDoc{Path: p}}
		content, ok, err := c.FileAt(ctx, ref.Repo, info.HeadSHA, p)
		if err != nil {
			return out, nil, github.UnreadableRepo(ref.Repo, err)
		}
		if !ok {
			continue
		}
		fm, _, _ := source.ReadFrontmatter(content)
		key := fm.Type
		if key == "" {
			key, _ = cfg.MappedProfile(p)
		}
		d.own = key != ""
		if key == "" {
			if g, sure := profile.Guess(a.Profiles(), content); sure {
				key = g
			}
			guessed := true
			d.Guessed = &guessed
		}
		if key != "" {
			d.Profile = &key
		}
		if t := docTitle(content); t != "" {
			d.Title = &t
		}
		docs = append(docs, d)
	}
	list := make([]api.ResolvedDoc, len(docs))
	for i, d := range docs {
		list[i] = d.ResolvedDoc
	}
	out.Docs = &list
	return out, docs, nil
}

// addPull makes the source of a pull request URL with the docs a person picked, and syncs it
// once. A pull request that a source already follows makes no second one.
func (a *API) addPull(ctx context.Context, ref github.Ref, res api.GithubResolved, picks *[]api.PullDocChoice) (api.AddGithubSourceResponseObject, error) {
	s := a.Service
	q := s.DB.Queries()
	if existing, err := q.ListGithubSourcesOfPull(ctx, pgdb.ListGithubSourcesOfPullParams{WorkspaceID: s.Workspace, Repo: ref.Repo,
		Pull: int64(ref.Pull)}); err != nil {
		return nil, err
	} else if len(existing) > 0 {
		return a.added(ctx, existing[len(existing)-1], ref, true)
	}
	if res.Pull.State != api.PullInfoStateOpen {
		return nil, kernel.Invalid("pull_not_open", "Pull request #%d of %s is %s. Speccy follows an open pull request.", ref.Pull, ref.Repo, res.Pull.State)
	}
	c, err := s.github(ctx, ref.APIURL())
	if err != nil {
		return nil, err
	}
	_, found, err := a.resolvePull(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	byPath := map[string]resolvedDoc{}
	for _, d := range found {
		byPath[d.Path] = d
	}
	// No choices: every file whose type Speccy knows or guesses, as the dialog prefills them.
	var choices []api.PullDocChoice
	if picks != nil {
		choices = *picks
	} else {
		for _, d := range found {
			p := ""
			if d.Profile != nil {
				p = *d.Profile
			}
			choices = append(choices, api.PullDocChoice{Path: d.Path, Profile: p})
		}
	}
	type pick struct {
		path, adopt string
		link        *api.ConfirmedLink
	}
	var picked []pick
	var notSpec []string
	// Every choice is checked before one is stored, so a bad one changes nothing (#73).
	for _, ch := range choices {
		d, ok := byPath[ch.Path]
		if !ok {
			return nil, kernel.Invalid("not_in_pull", "%s is not a markdown file that pull request #%d changes.", ch.Path, ref.Pull)
		}
		key := strings.TrimSpace(ch.Profile)
		if key == "" {
			notSpec = append(notSpec, ch.Path)
			continue
		}
		if _, ok := a.Profiles()[key]; !ok {
			return nil, kernel.Invalid("no_such_profile", "There is no profile %q. The profiles are: %s.", key, strings.Join(a.profileKeys(), ", "))
		}
		if l := ch.Link; l != nil && !checkLinkKind(l.Kind) {
			return nil, kernel.Invalid("bad_link", "There is no link kind %q.", l.Kind)
		}
		p := pick{path: ch.Path, link: ch.Link}
		if !d.own {
			p.adopt = key // a doc that names no type takes the profile as its adopted type
		}
		picked = append(picked, p)
	}
	if len(picked) == 0 {
		return nil, kernel.Invalid("no_spec_doc", "Pick a profile for at least one doc that pull request #%d changes.", ref.Pull)
	}
	paths := make([]string, len(picked))
	for i, p := range picked {
		paths[i] = p.path
	}
	sort.Strings(paths)
	docsJSON, _ := json.Marshal(paths)
	now := time.Now().UTC()
	by := kernel.ActorFrom(ctx).UserID
	src := pgdb.InsertGithubSourceParams{ID: kernel.NewID(), WorkspaceID: s.Workspace, Repo: ref.Repo, Branch: res.Pull.HeadBranch,
		Path: ".", ApiUrl: ref.APIURL(), CreatedBy: by, CreatedAt: now}
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		q := tx.Queries()
		if err := q.InsertGithubSource(ctx, src); err != nil {
			return err
		}
		if err := q.SetGithubSourcePull(ctx, pgdb.SetGithubSourcePullParams{ID: src.ID, Pull: int64(ref.Pull), PullTitle: res.Pull.Title,
			PullAuthor: res.Pull.Author, PullState: string(res.Pull.State), BaseBranch: res.Pull.BaseBranch, Branch: res.Pull.HeadBranch,
			Docs: dbtype.JSON(docsJSON)}); err != nil {
			return err
		}
		for _, p := range picked {
			if p.adopt != "" {
				if err := q.SetAdoptedType(ctx, pgdb.SetAdoptedTypeParams{SourceID: src.ID, Path: p.path, Profile: p.adopt}); err != nil {
					return err
				}
			}
			if l := p.link; l != nil {
				if err := q.SetAdoptedLink(ctx, pgdb.SetAdoptedLinkParams{SourceID: src.ID, Path: p.path, Kind: l.Kind, Target: path.Clean(l.Target)}); err != nil {
					return err
				}
			}
		}
		// "Not a spec": the file stays out of the list of skipped docs of this source.
		for _, p := range notSpec {
			if err := q.InsertDismissedDoc(ctx, pgdb.InsertDismissedDocParams{WorkspaceID: s.Workspace, SourceID: uuid.NullUUID{UUID: src.ID, Valid: true},
				Path: p, DismissedBy: by, CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A failed first sync keeps the source, with its error, as for a branch source (#67).
	_ = s.SyncSource(ctx, src.ID, true)
	row, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: src.ID})
	if err != nil {
		return nil, err
	}
	return a.added(ctx, row, ref, false)
}
