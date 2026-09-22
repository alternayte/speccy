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

// github returns the client for a source's host. Local mode takes the token from the machine's
// gh login, and hosted mode from the workspace connection (REQ-129).
func (s *Service) github(ctx context.Context, apiURL string) (*github.Client, error) {
	if s.GitHub == nil {
		return nil, kernel.Invalid("github_unavailable", "Speccy has no way to reach GitHub here.")
	}
	return s.GitHub(ctx, apiURL)
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

// WatchGitHub syncs the GitHub sources every interval until ctx ends, in local mode and in
// hosted mode. The token has no webhooks (DEC-019), so Speccy polls. A source that has no
// bundles yet, or whose last sync failed, is tried again like any other.
func (s *Service) WatchGitHub(ctx context.Context, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
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
	c, err := s.github(ctx, src.ApiUrl)
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
		// The sidecars sit at the root of the repo, outside the source's path (DEC-009).
		return p == source.RepoConfigFile || source.IsSidecar(p) || readable(src, p)
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
	if src.IsFile {
		// REQ-128: a one-doc source maps its doc, so the scan makes a single-file bundle. A
		// type in the doc, or a mapping in the repo, still wins (REQ-130).
		cfg = mapOneDoc(cfg, src)
	}
	// REQ-133: the types a person accepted in the app, for the docs the repo names none for.
	// The repo wins, so a path the repo now maps drops its row. repoCfg is the repo's own
	// answer, without the mappings Speccy adds, so a doc does not look mapped to itself.
	repoCfg := cfg
	adopted, err := s.DB.Queries().ListAdoptedTypes(ctx, src.ID)
	if err != nil {
		return err
	}
	for _, a := range adopted {
		if key, ok := cfg.MappedProfile(a.Path); ok && key != "" {
			if err := s.DB.Queries().DeleteAdoptedType(ctx, pgdb.DeleteAdoptedTypeParams{SourceID: src.ID, Path: a.Path}); err != nil {
				return err
			}
			continue
		}
		cfg.Map = append(cfg.Map, source.Mapping{Glob: a.Path, Profile: a.Profile})
	}
	scan, err := local.FromFS(tfs).Scan(cfg)
	if err != nil {
		return fail(err)
	}
	taken, err := s.applyGitHubScan(ctx, src, commit, cfg, scan, tfs)
	if err != nil {
		return fail(err)
	}
	// The files the scan passed over, so the app lists them with no second read of the tree.
	skippedJSON, _ := json.Marshal(nonNilPaths(SkippedUnder(src, scan)))
	if err := q.SetGithubSourceSkipped(ctx, pgdb.SetGithubSourceSkippedParams{ID: src.ID, Skipped: dbtype.JSON(skippedJSON)}); err != nil {
		return err
	}
	// A doc that gained its own type in the repo no longer needs the row in Speccy.
	if err := s.dropAdoptedWithType(ctx, src, repoCfg, tfs); err != nil {
		return err
	}
	// A repo is other people's tree: Speccy writes no type into it. It says which files need
	// one, instead of finding nothing in silence.
	if err := noBundleHere(src, scan); err != nil {
		return fail(err)
	}
	// A doc another source already holds makes no bundle here. Say so: a person who accepted a
	// type for it would otherwise read "accepted" and find nothing.
	if len(taken) > 0 {
		return fail(alreadyHeld(taken))
	}
	if err := q.SetGithubSourceSynced(ctx, pgdb.SetGithubSourceSyncedParams{ID: src.ID, HeadCommit: commit,
		SyncedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true}}); err != nil {
		return err
	}
	return s.afterChange(ctx)
}

// applyGitHubScan writes the bundles of a scan. It returns the docs it did not take, because a
// bundle of that slug belongs to another source: a person who accepted a type must not be told
// it worked while nothing appears.
func (s *Service) applyGitHubScan(ctx context.Context, src pgdb.GithubSource, commit string, cfg source.RepoConfig, scan *local.Scan, tfs fs.FS) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var taken []string
	now := time.Now().UTC()
	message := "From GitHub " + short(commit)
	found := map[string]bool{}
	for _, fb := range scan.Bundles {
		if !bundleOfSource(src, fb) {
			continue
		}
		found[fb.Slug] = true
		// The sidecar of the main doc travels with the bundle, so a waiver merged in the repo
		// reaches the app, and a publish writes it back to the root (DEC-009).
		if raw, err := fs.ReadFile(tfs, source.SidecarPath(path.Join(fb.Dir, fb.Main.Path))); err == nil {
			fb.Files = append(fb.Files, source.File{Path: source.SidecarPath(fb.Main.Path), Content: raw})
			source.Sort(fb.Files)
		}
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
				taken = append(taken, path.Join(fb.Dir, fb.Main.Path))
				return nil
			} else {
				_ = json.Unmarshal(b.SourceRef, &ref)
				if ref.Source != src.ID {
					taken = append(taken, path.Join(fb.Dir, fb.Main.Path))
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
			return taken, err
		}
	}
	existing, err := s.DB.Queries().ListBundlesBySource(ctx, pgdb.ListBundlesBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return taken, err
	}
	for _, b := range existing {
		var ref githubRef
		_ = json.Unmarshal(b.SourceRef, &ref)
		if ref.Source == src.ID && !found[b.Slug] && !b.ArchivedAt.Valid {
			if err := s.DB.Queries().SetBundleArchived(ctx, pgdb.SetBundleArchivedParams{ID: b.ID,
				ArchivedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now}); err != nil {
				return taken, err
			}
		}
	}
	return taken, nil
}

// underSource says whether a repo path belongs to the source: anything in its folder, or the
// doc of a one-doc source and the files of that doc's assets folder (REQ-128).
func underSource(src pgdb.GithubSource, p string) bool {
	if !src.IsFile {
		return github.Under(p, src.Path)
	}
	return p == src.Path || github.Under(p, path.Join(path.Dir(src.Path), source.AssetsDir(path.Base(src.Path))))
}

// readable says which paths the scan may read. It is wider than underSource: a bundle carries
// the files its doc references, and those sit beside the doc or below it. The scan decides
// what a bundle takes; this decides only what Speccy fetches.
func readable(src pgdb.GithubSource, p string) bool {
	if underSource(src, p) {
		return true
	}
	if !src.IsFile {
		return false
	}
	return github.Under(p, path.Dir(src.Path))
}

// bundleOfSource says whether a scanned bundle belongs to the source.
func bundleOfSource(src pgdb.GithubSource, b local.Bundle) bool {
	if !src.IsFile {
		return github.Under(b.Slug, src.Path) || github.Under(b.Dir, src.Path)
	}
	return path.Join(b.Dir, b.File) == src.Path
}

// mapOneDoc adds the mapping of a one-doc source, unless the repo already maps that doc.
func mapOneDoc(cfg source.RepoConfig, src pgdb.GithubSource) source.RepoConfig {
	if src.Profile == "" {
		return cfg
	}
	if key, ok := cfg.MappedProfile(src.Path); ok && key != "" {
		return cfg
	}
	cfg.Map = append(cfg.Map, source.Mapping{Glob: src.Path, Profile: src.Profile})
	return cfg
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
	c, err := s.github(ctx, src.ApiUrl)
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
	changes := diffChanges(ref.Dir, b.MainDoc, published, current)
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

// diffChanges lists the files that differ between two versions, as repo paths. The sidecar is
// the one file that does not sit under the bundle's folder: it goes to the root of the repo,
// under the main doc's repo path (DEC-009).
func diffChanges(dir, mainDoc string, from, to []source.File) []github.Change {
	repoPath := func(p string) string {
		if p == source.SidecarPath(mainDoc) {
			return source.SidecarPath(path.Join(dir, mainDoc))
		}
		return path.Join(dir, p)
	}
	old := map[string]string{}
	for _, f := range from {
		old[f.Path] = version.Hash(f.Content)
	}
	var out []github.Change
	seen := map[string]bool{}
	for _, f := range to {
		seen[f.Path] = true
		// A carried file belongs to the repo, and another bundle may carry the same one.
		// Speccy never writes it back.
		if f.Carried() {
			continue
		}
		if h, ok := old[f.Path]; !ok || h != version.Hash(f.Content) {
			content := f.Content
			if content == nil {
				content = []byte{}
			}
			out = append(out, github.Change{Path: repoPath(f.Path), Content: content})
		}
	}
	for _, f := range from {
		if !seen[f.Path] && !f.Carried() {
			out = append(out, github.Change{Path: repoPath(f.Path)})
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

// nonNilPaths keeps the JSON an array, never null.
func nonNilPaths(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// dropAdoptedWithType removes the accepted type of a doc that the repo now types itself, by
// the doc's own frontmatter or by a mapping in the repo's .speccy.yaml. It reads the file, not
// the scan: the scan's type comes from the mapping Speccy adds for the accepted type.
func (s *Service) dropAdoptedWithType(ctx context.Context, src pgdb.GithubSource, repoCfg source.RepoConfig, tfs fs.FS) error {
	q := s.DB.Queries()
	adopted, err := q.ListAdoptedTypes(ctx, src.ID)
	if err != nil {
		return err
	}
	for _, a := range adopted {
		byRepo := false
		if key, ok := repoCfg.MappedProfile(a.Path); ok && key != "" {
			byRepo = true
		}
		if !byRepo {
			raw, err := fs.ReadFile(tfs, a.Path)
			if err != nil {
				continue
			}
			fm, _, err := source.ReadFrontmatter(raw)
			byRepo = err == nil && fm.Type != ""
		}
		if !byRepo {
			continue
		}
		if err := q.DeleteAdoptedType(ctx, pgdb.DeleteAdoptedTypeParams{SourceID: src.ID, Path: a.Path}); err != nil {
			return err
		}
	}
	return nil
}

// SkippedUnder returns the markdown files under the source that the scan passed over.
func SkippedUnder(src pgdb.GithubSource, scan *local.Scan) []string {
	var out []string
	for _, p := range scan.Skipped {
		if underSource(src, p) {
			out = append(out, p)
		}
	}
	return out
}

// alreadyHeld names the docs another source holds, and what to do about it.
func alreadyHeld(taken []string) error {
	sort.Strings(taken)
	shown := taken
	if len(shown) > 3 {
		shown = append(shown[:3:3], "and more")
	}
	return fmt.Errorf("another source already holds %s, so this source makes no bundle for %s. Remove the other source, or point this one at a folder it does not cover",
		strings.Join(shown, ", "), plural2(len(taken), "that doc", "those docs"))
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// noBundleHere reports the markdown files under a source's path that name no type, when the
// source has no bundle at all.
func noBundleHere(src pgdb.GithubSource, scan *local.Scan) error {
	for _, b := range scan.Bundles {
		if bundleOfSource(src, b) {
			return nil
		}
	}
	var skipped []string
	for _, p := range scan.Skipped {
		if underSource(src, p) {
			skipped = append(skipped, p)
		}
	}
	if len(skipped) == 0 {
		return nil
	}
	if len(skipped) > 3 {
		skipped = append(skipped[:3:3], "and more")
	}
	return fmt.Errorf("no file under %s names a type, so this source has no bundle. Add \"type: <key>\" to the frontmatter of %s in the repo",
		src.Path, strings.Join(skipped, ", "))
}

// PublishMapping opens a pull request that writes the accepted types into the repo's
// .speccy.yaml (REQ-133). It writes one mapping for each folder whose accepted docs share a
// type, and one for each other doc. It changes no doc and writes no workflow.
func (s *Service) PublishMapping(ctx context.Context, id uuid.UUID, by string) (github.PullRequest, error) {
	q := s.DB.Queries()
	src, err := q.GetGithubSource(ctx, pgdb.GetGithubSourceParams{WorkspaceID: s.Workspace, ID: id})
	if err != nil {
		return github.PullRequest{}, err
	}
	adopted, err := q.ListAdoptedTypes(ctx, src.ID)
	if err != nil {
		return github.PullRequest{}, err
	}
	if len(adopted) == 0 {
		return github.PullRequest{}, kernel.Invalid("nothing_adopted", "No doc of this source has a type accepted in Speccy, so there is nothing to write.")
	}
	c, err := s.github(ctx, src.ApiUrl)
	if err != nil {
		return github.PullRequest{}, err
	}
	cfg := source.RepoConfig{}
	raw, ok, err := c.FileAt(ctx, src.Repo, src.Branch, source.RepoConfigFile)
	if err != nil {
		return github.PullRequest{}, err
	}
	if ok {
		if cfg, err = source.ParseRepoConfig(raw); err != nil {
			return github.PullRequest{}, kernel.Invalid("bad_repo_config", "%s in the repo is not valid: %s.", source.RepoConfigFile, err.Error())
		}
	}
	var add []source.Mapping
	var skipped []string
	_ = json.Unmarshal(src.Skipped, &skipped)
	for _, m := range mappingsFor(adopted, skipped) {
		if key, has := cfg.MappedProfile(m.Glob); has && key == m.Profile {
			continue
		}
		add = append(add, m)
	}
	if len(add) == 0 {
		return github.PullRequest{}, kernel.Invalid("already_mapped", "The repo already maps every doc this source adopted.")
	}
	next, err := source.AddMappings(raw, add)
	if err != nil {
		return github.PullRequest{}, err
	}
	branch := fmt.Sprintf("speccy/mapping-%s", time.Now().UTC().Format("20060102150405"))
	title := fmt.Sprintf("Map %d doc%s for Speccy", len(adopted), plural(len(adopted)))
	body := fmt.Sprintf("Opened from Speccy by %s.\n\nIt adds mappings to `%s`, so the Speccy Action reads the same doc types as the app. It changes no doc.",
		by, source.RepoConfigFile)
	pr, err := c.Publish(ctx, src.Repo, src.Branch, src.HeadCommit, branch, title, title, body,
		[]github.Change{{Path: source.RepoConfigFile, Content: next}})
	if err != nil {
		return pr, kernel.Invalid("publish_failed", "The pull request was not opened: %s.", strings.TrimSuffix(err.Error(), "."))
	}
	return pr, nil
}

// mappingsFor turns the accepted types into mappings: one glob per folder when every doc the
// scan passed over there was accepted with the same type, and one per doc otherwise. A folder
// glob would otherwise take in a doc the person left alone. skipped is every passed-over doc
// of the source.
func mappingsFor(adopted []pgdb.AdoptedType, skipped []string) []source.Mapping {
	byDir := map[string]map[string]bool{}
	accepted := map[string]bool{}
	for _, a := range adopted {
		dir := path.Dir(a.Path)
		if byDir[dir] == nil {
			byDir[dir] = map[string]bool{}
		}
		byDir[dir][a.Profile] = true
		accepted[a.Path] = true
	}
	// left counts the docs of a folder that the person did not accept.
	left := map[string]int{}
	for _, p := range skipped {
		if !accepted[p] {
			left[path.Dir(p)]++
		}
	}
	var out []source.Mapping
	seen := map[string]bool{}
	for _, a := range adopted {
		dir := path.Dir(a.Path)
		if len(byDir[dir]) == 1 && dir != "." && left[dir] == 0 {
			glob := path.Join(dir, "*.md")
			if !seen[glob] {
				seen[glob] = true
				out = append(out, source.Mapping{Glob: glob, Profile: a.Profile})
			}
			continue
		}
		if !seen[a.Path] {
			seen[a.Path] = true
			out = append(out, source.Mapping{Glob: a.Path, Profile: a.Profile})
		}
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
