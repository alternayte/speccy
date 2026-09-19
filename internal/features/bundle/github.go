package bundle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/features/version"
	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/source/local"
	"github.com/alternayte/speccy/internal/store"
)

// KindGitHub is a bundle read from a GitHub repo (REQ-123).
const KindGitHub = "github"

// GitHubUser is created_by for versions read from GitHub.
const GitHubUser = "github"

// githubRef is source_ref for a GitHub bundle. The bundle's current version is a draft when it
// is not the published version: the version that matches GitHub.
type githubRef struct {
	Source    uuid.UUID `json:"source_id"`
	Dir       string    `json:"dir"`
	File      string    `json:"file,omitempty"`
	Profile   string    `json:"profile,omitempty"` // the mapped profile of a single-file bundle
	Commit    string    `json:"commit"`            // the commit of the published version
	Published uuid.UUID `json:"published_version_id"`
	// Ahead is set when GitHub changed while the bundle has a draft.
	Ahead    bool   `json:"ahead,omitempty"`
	PR       string `json:"pr_url,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
}

// GitHubState is what the app shows of a GitHub bundle.
type GitHubState struct {
	Repo, Branch, Path string
	Draft, Ahead       bool
	PR                 string
}

// GitHubStateOf returns the GitHub state of b, or false for another source.
func GitHubStateOf(ctx context.Context, q store.Querier, b pgdb.Bundle) (GitHubState, bool) {
	if b.SourceKind != KindGitHub {
		return GitHubState{}, false
	}
	var ref githubRef
	_ = json.Unmarshal(b.SourceRef, &ref)
	src, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: b.WorkspaceID, ID: ref.Source})
	if err != nil {
		return GitHubState{}, false
	}
	return GitHubState{Repo: src.Repo, Branch: src.Branch, Path: src.Path, Draft: b.CurrentVersionID.UUID != ref.Published,
		Ahead: ref.Ahead, PR: ref.PR}, true
}

// blobCache keeps GitHub blobs by their SHA, so a sync fetches only new content.
type blobCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *blobCache) get(sha string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[sha]
	return b, ok
}

func (c *blobCache) put(sha string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string][]byte{}
	}
	c.m[sha] = b
}

func (s *Service) github(ctx context.Context) (*github.Client, error) {
	if s.GitHub == nil {
		return nil, kernel.Invalid("github_unavailable", "GitHub sources are for hosted mode. In local mode, clone the repo and run speccy in it.")
	}
	return s.GitHub(ctx)
}

// SyncGitHub reads every GitHub source. A source that fails keeps its error for the app.
func (s *Service) SyncGitHub(ctx context.Context) error {
	srcs, err := s.DB.Queries().ListGithubSources(ctx, s.Workspace)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		if err := s.SyncSource(ctx, src.ID, false); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "sync of a GitHub source failed", "repo", src.Repo, "branch", src.Branch, "err", err)
		}
	}
	return nil
}

// WatchGitHub syncs the GitHub sources every interval until ctx ends. The token has no
// webhooks (DEC-019), so Speccy polls.
func (s *Service) WatchGitHub(ctx context.Context, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if _, err := s.DB.Queries().GetGithubConnection(ctx, s.Workspace); err != nil {
			continue
		}
		_ = s.SyncGitHub(ctx)
	}
}

// SyncSource reads the source's branch and records a version for each bundle that changed on
// GitHub. force reads the tree even when the branch has not moved.
func (s *Service) SyncSource(ctx context.Context, id uuid.UUID, force bool) error {
	q := s.DB.Queries()
	src, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return kernel.NotFound("source_not_found", "No GitHub source has this ID.")
	}
	if err != nil {
		return err
	}
	c, err := s.github(ctx)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = q.SetGithubSourceSynced(ctx, pgdb.SetGithubSourceSyncedParams{ID: src.ID, HeadCommit: src.HeadCommit,
			SyncedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}, Error: sentence(err.Error())})
		return err
	}
	commit, tree, err := c.Head(ctx, src.Repo, src.Branch)
	if err != nil {
		return fail(err)
	}
	if commit == src.HeadCommit && src.Error == "" && !force {
		return nil
	}
	entries, truncated, err := c.Tree(ctx, src.Repo, tree)
	if err != nil {
		return fail(err)
	}
	if truncated {
		return fail(fmt.Errorf("the repo tree is too large for the GitHub API. Point the source at a smaller folder"))
	}
	tfs := github.NewTreeFS(entries, func(p string) bool {
		return p == source.RepoConfigFile || github.Under(p, src.Path)
	}, func(e github.Entry) ([]byte, error) {
		if b, ok := s.blobs.get(e.SHA); ok {
			return b, nil
		}
		b, err := c.Blob(ctx, src.Repo, e.SHA)
		if err == nil {
			s.blobs.put(e.SHA, b)
		}
		return b, err
	})
	cfg := source.RepoConfig{}
	if raw, err := fs.ReadFile(tfs, source.RepoConfigFile); err == nil {
		if cfg, err = source.ParseRepoConfig(raw); err != nil {
			return fail(fmt.Errorf("%s in the repo is not valid: %w", source.RepoConfigFile, err))
		}
	}
	scan, err := local.FromFS(tfs).Scan(cfg)
	if err != nil {
		return fail(err)
	}
	if err := s.applyGitHubScan(ctx, src, commit, cfg, scan); err != nil {
		return fail(err)
	}
	if err := q.SetGithubSourceSynced(ctx, pgdb.SetGithubSourceSyncedParams{ID: src.ID, HeadCommit: commit,
		SyncedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}}); err != nil {
		return err
	}
	return s.afterChange(ctx)
}

func (s *Service) applyGitHubScan(ctx context.Context, src pgdb.GithubSource, commit string, cfg source.RepoConfig, scan *local.Scan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	message := "From GitHub " + short(commit)
	found := map[string]bool{}
	for _, fb := range scan.Bundles {
		if !github.Under(fb.Slug, src.Path) && !github.Under(fb.Dir, src.Path) {
			continue
		}
		found[fb.Slug] = true
		mapped := ""
		if fb.File != "" {
			mapped, _ = cfg.MappedProfile(path.Join(fb.Dir, fb.File))
		}
		err := s.DB.InTx(ctx, func(tx store.Tx) error {
			q := tx.Queries()
			b, err := q.GetBundleBySlug(ctx, pgdb.GetBundleBySlugParams{WorkspaceID: s.Workspace, Slug: fb.Slug})
			var ref githubRef
			if errors.Is(err, sql.ErrNoRows) {
				ref = githubRef{Source: src.ID, Dir: fb.Dir, File: fb.File, Profile: mapped}
				raw, _ := json.Marshal(ref)
				b = pgdb.Bundle{ID: kernel.NewID(), WorkspaceID: s.Workspace, Slug: fb.Slug, Title: s.title(fb.Main, fb.Slug),
					ProfileKey: fb.Main.Frontmatter.Type, MainDoc: fb.Main.Path, SourceKind: KindGitHub, SourceRef: dbtype.JSON(raw),
					CreatedAt: now, UpdatedAt: now}
				if err := q.InsertBundle(ctx, insertParams(b)); err != nil {
					return err
				}
				// The admin who added the source is the author of its bundles (SDD §3).
				if err := q.InsertBundleAuthor(ctx, pgdb.InsertBundleAuthorParams{BundleID: b.ID, UserID: src.CreatedBy}); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if b.SourceKind != KindGitHub {
				return nil // another source has this slug; the scan problem list says so below
			} else {
				_ = json.Unmarshal(b.SourceRef, &ref)
				if ref.Source != src.ID {
					return nil
				}
			}
			if b.ArchivedAt.Valid {
				if err := q.SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID, UpdatedAt: now}); err != nil {
					return err
				}
			}
			draft := b.CurrentVersionID.Valid && b.CurrentVersionID.UUID != ref.Published
			if !draft {
				v, _, err := version.Record(ctx, tx, version.Change{Bundle: b, Files: fb.Files, Title: s.title(fb.Main, fb.Slug),
					Profile: fb.Main.Frontmatter.Type, MainDoc: fb.Main.Path, CreatedBy: GitHubUser, Message: message})
				if err != nil {
					return err
				}
				ref.Published, ref.Commit, ref.Ahead, ref.PR, ref.PRNumber = v.ID, commit, false, "", 0
			} else {
				current, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
				if err != nil {
					return err
				}
				published, err := version.Files(ctx, q, ref.Published)
				if err != nil {
					return err
				}
				switch {
				case sameFiles(current, fb.Files):
					// The draft reached GitHub, for example through its pull request.
					ref.Published, ref.Commit, ref.Ahead, ref.PR, ref.PRNumber = b.CurrentVersionID.UUID, commit, false, "", 0
				case !sameFiles(published, fb.Files):
					ref.Ahead = true
				}
			}
			ref.Profile = mapped
			raw, _ := json.Marshal(ref)
			return q.SetBundleSourceRef(ctx, pgdb.SetBundleSourceRefParams{ID: b.ID, SourceRef: dbtype.JSON(raw), UpdatedAt: now})
		})
		if err != nil {
			return err
		}
	}
	existing, err := s.DB.Queries().ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return err
	}
	for _, b := range existing {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == src.ID && !found[b.Slug] && !b.ArchivedAt.Valid {
			if err := s.DB.Queries().SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID,
				ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now}); err != nil {
				return err
			}
		}
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// sameFiles reports whether two file sets have the same paths and contents.
func sameFiles(a, b []source.File) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, f := range a {
		m[f.Path] = version.Hash(f.Content)
	}
	for _, f := range b {
		if h, ok := m[f.Path]; !ok || h != version.Hash(f.Content) {
			return false
		}
	}
	return true
}

var branchUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Publish commits the bundle's draft to a new branch and opens a pull request into the
// source branch (REQ-123). The source branch itself never changes here.
func (s *Service) Publish(ctx context.Context, id uuid.UUID, by, message string) (github.PullRequest, error) {
	q := s.DB.Queries()
	b, err := version.Bundle(ctx, q, s.Workspace, id)
	if err != nil {
		return github.PullRequest{}, err
	}
	if b.SourceKind != KindGitHub {
		return github.PullRequest{}, kernel.Invalid("not_github", "Only a bundle from GitHub is published as a pull request.")
	}
	var ref githubRef
	_ = json.Unmarshal(b.SourceRef, &ref)
	if b.CurrentVersionID.UUID == ref.Published {
		return github.PullRequest{}, kernel.Invalid("no_draft", "The bundle has no changes since it came from GitHub. Edit it first.")
	}
	src, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: ref.Source})
	if err != nil {
		return github.PullRequest{}, err
	}
	c, err := s.github(ctx)
	if err != nil {
		return github.PullRequest{}, err
	}
	current, err := version.Files(ctx, q, b.CurrentVersionID.UUID)
	if err != nil {
		return github.PullRequest{}, err
	}
	published, err := version.Files(ctx, q, ref.Published)
	if err != nil {
		return github.PullRequest{}, err
	}
	changes := diffChanges(ref.Dir, published, current)
	v, err := q.GetVersion(ctx, pgdb.GetVersionParams{BundleID: b.ID, ID: b.CurrentVersionID.UUID})
	if err != nil {
		return github.PullRequest{}, err
	}
	if strings.TrimSpace(message) == "" {
		message = "Update " + b.Title
	}
	branch := fmt.Sprintf("speccy/%s-v%d-%s", strings.Trim(branchUnsafe.ReplaceAllString(b.Slug, "-"), "-"), v.Number, time.Now().UTC().Format("20060102150405"))
	body := fmt.Sprintf("Published from Speccy: %s, version %d, by %s.\n\nFiles: %s.", b.Title, v.Number, by, changedPaths(changes))
	pr, err := c.Publish(ctx, src.Repo, src.Branch, ref.Commit, branch, message, message, body, changes)
	if err != nil {
		return pr, kernel.Invalid("publish_failed", "The pull request was not opened: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	ref.PR, ref.PRNumber = pr.URL, pr.Number
	raw, _ := json.Marshal(ref)
	if err := q.SetBundleSourceRef(ctx, pgdb.SetBundleSourceRefParams{ID: b.ID, SourceRef: dbtype.JSON(raw), UpdatedAt: time.Now().UTC()}); err != nil {
		return pr, err
	}
	return pr, nil
}

// diffChanges lists the files that differ between two versions, as repo paths.
func diffChanges(dir string, from, to []source.File) []github.Change {
	old := map[string]string{}
	for _, f := range from {
		old[f.Path] = version.Hash(f.Content)
	}
	var out []github.Change
	seen := map[string]bool{}
	for _, f := range to {
		seen[f.Path] = true
		if h, ok := old[f.Path]; !ok || h != version.Hash(f.Content) {
			content := f.Content
			if content == nil {
				content = []byte{}
			}
			out = append(out, github.Change{Path: path.Join(dir, f.Path), Content: content})
		}
	}
	for _, f := range from {
		if !seen[f.Path] {
			out = append(out, github.Change{Path: path.Join(dir, f.Path)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func changedPaths(cs []github.Change) string {
	ps := make([]string, len(cs))
	for i, c := range cs {
		ps[i] = "`" + c.Path + "`"
	}
	return strings.Join(ps, ", ")
}

// DiscardDraft drops the bundle's draft: the published version becomes current again, as a new
// version. A sync then brings any newer GitHub changes.
func (s *Service) DiscardDraft(ctx context.Context, id uuid.UUID, by string) (pgdb.Version, error) {
	q := s.DB.Queries()
	b, err := version.Bundle(ctx, q, s.Workspace, id)
	if err != nil {
		return pgdb.Version{}, err
	}
	var ref githubRef
	_ = json.Unmarshal(b.SourceRef, &ref)
	if b.SourceKind != KindGitHub || b.CurrentVersionID.UUID == ref.Published {
		return pgdb.Version{}, kernel.Invalid("no_draft", "The bundle has no draft to discard.")
	}
	files, err := version.Files(ctx, q, ref.Published)
	if err != nil {
		return pgdb.Version{}, err
	}
	main, err := s.mainDoc(b, files)
	if err != nil {
		return pgdb.Version{}, err
	}
	var v pgdb.Version
	s.mu.Lock()
	err = s.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		if v, _, err = version.Record(ctx, tx, version.Change{Bundle: b, Files: files, Title: s.title(main, b.Slug),
			Profile: main.Frontmatter.Type, MainDoc: main.Path, CreatedBy: by, Message: "Discarded the draft"}); err != nil {
			return err
		}
		ref.Published, ref.PR, ref.PRNumber = v.ID, "", 0
		raw, _ := json.Marshal(ref)
		return tx.Queries().SetBundleSourceRef(ctx, pgdb.SetBundleSourceRefParams{ID: b.ID, SourceRef: dbtype.JSON(raw), UpdatedAt: time.Now().UTC()})
	})
	s.mu.Unlock()
	if err != nil {
		return v, err
	}
	if ref.Ahead {
		_ = s.SyncSource(ctx, ref.Source, true)
	}
	return v, s.afterChange(ctx)
}
