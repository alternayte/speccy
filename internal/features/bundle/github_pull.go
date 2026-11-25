package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/findingkey"
	"github.com/alternayte/speccy/internal/http/api"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/store"
)

// The states of the pull request of a pull request source (docs/specs/pr-source-and-github-review.md).
const (
	PullOpen   = "open"
	PullMerged = "merged"
	PullClosed = "closed"
)

// The reasons a doc of a pull request source is read-only.
const (
	ReadOnlyClosed = "closed" // the pull request closed without a merge
	ReadOnlyMerged = "merged" // it merged, and another bundle holds the doc on the base branch
)

// isPull reports whether the source follows a pull request.
func isPull(src pgdb.GithubSource) bool { return src.Pull > 0 }

// pullDocs are the repo paths of the spec docs a person picked for a pull request source.
func pullDocs(src pgdb.GithubSource) map[string]bool {
	var paths []string
	_ = json.Unmarshal(src.Docs, &paths)
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out
}

// pullDirs are the folders of the picked docs: the scan reads these and nothing else.
func pullDirs(src pgdb.GithubSource) []string {
	seen := map[string]bool{}
	var out []string
	for p := range pullDocs(src) {
		if d := path.Dir(p); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// pullSlug keeps the bundles of a pull request apart from the bundles of its base branch, which
// hold the same paths.
func pullSlug(slug string, pull int64) string {
	if slug == "." || slug == "" {
		return fmt.Sprintf("pr-%d", pull)
	}
	return fmt.Sprintf("%s-pr-%d", slug, pull)
}

// pullInfo is the pull request of a source for the API, or nil for a branch source.
func pullInfo(src pgdb.GithubSource) *api.PullInfo {
	if !isPull(src) {
		return nil
	}
	head := src.Branch
	if src.PullState == PullMerged {
		head = ""
	}
	return &api.PullInfo{Number: int(src.Pull), Title: src.PullTitle, Url: pullURL(src), Author: src.PullAuthor,
		State: api.PullInfoState(src.PullState), HeadBranch: head, BaseBranch: src.BaseBranch}
}

// pullURL is the web address of the source's pull request.
func pullURL(src pgdb.GithubSource) string {
	return fmt.Sprintf("%s/%s/pull/%d", github.WebURL(src.ApiUrl), src.Repo, src.Pull)
}

// changedMarkdown are the markdown files a pull request adds or changes.
func changedMarkdown(files []github.PRFile) []string {
	var out []string
	for _, f := range files {
		if f.Status != "removed" && source.IsMarkdown(f.Filename) && !source.IsSidecar(f.Filename) {
			out = append(out, f.Filename)
		}
	}
	return out
}

// pullHead is what a sync of a pull request source reads: the commit and tree, and the
// markdown files the pull request changes. stop is set when the source no longer syncs.
type pullHead struct {
	commit, tree string
	changed      []string
	stop         bool
}

// readPull reads the source's pull request and records what changed: the title, the state, the
// branch. A merge moves the source to the base branch, and a close stops it. src is updated.
func (s *Service) readPull(ctx context.Context, c *github.Client, src *pgdb.GithubSource) (pullHead, error) {
	info, err := c.PullRequest(ctx, src.Repo, int(src.Pull))
	if err != nil {
		return pullHead{}, err
	}
	prev := src.PullState
	var out pullHead
	branch := src.Branch
	switch info.State {
	case PullOpen:
		branch = info.HeadRef
		out.commit = info.HeadSHA
		// The base repo serves the head commit, for a fork too.
		if _, out.tree, err = c.CommitTree(ctx, src.Repo, info.HeadSHA); err != nil {
			return out, err
		}
		files, err := c.PRFiles(ctx, src.Repo, int(src.Pull))
		if err != nil {
			return out, err
		}
		out.changed = changedMarkdown(files)
		if prev == PullClosed {
			if err := s.liftReadOnly(ctx, src.ID, ReadOnlyClosed); err != nil {
				return out, err
			}
		}
	case PullMerged:
		branch = info.BaseRef
		if out.commit, out.tree, err = c.Head(ctx, src.Repo, info.BaseRef); err != nil {
			return out, err
		}
	default:
		out.stop = true
	}
	docs, _ := json.Marshal(sortedKeys(pullDocs(*src)))
	p := pgdb.SetGithubSourcePullParams{ID: src.ID, Pull: src.Pull, PullTitle: info.Title, PullAuthor: info.Author,
		PullAuthorID: info.AuthorID, PullState: info.State, BaseBranch: info.BaseRef, Branch: branch, Docs: dbtype.JSON(docs)}
	if err := s.DB.Queries().SetGithubSourcePull(ctx, p); err != nil {
		return out, err
	}
	src.PullTitle, src.PullAuthor, src.PullAuthorID, src.PullState, src.BaseBranch, src.Branch = info.Title, info.Author, info.AuthorID, info.State, info.BaseRef, branch
	switch {
	case info.State == PullClosed && prev != PullClosed:
		if err := s.freezePull(ctx, *src, ReadOnlyClosed, nil); err != nil {
			return out, err
		}
	case info.State == PullMerged && prev != PullMerged:
		if err := s.mergePull(ctx, *src); err != nil {
			return out, err
		}
	}
	return out, nil
}

// sourceSpecDocs are the live spec docs of a source.
func (s *Service) sourceSpecDocs(ctx context.Context, q store.Querier, id uuid.UUID) ([]pgdb.SpecDoc, error) {
	docs, err := q.ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return nil, err
	}
	var out []pgdb.SpecDoc
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		if ref.Source == id && !d.ArchivedAt.Valid {
			out = append(out, d)
		}
	}
	return out, nil
}

// freezePull makes docs of a pull request source read-only for reason. moved names, by spec doc,
// the spec doc that holds it on the base branch. A nil moved freezes every doc.
func (s *Service) freezePull(ctx context.Context, src pgdb.GithubSource, reason string, moved map[uuid.UUID]uuid.UUID) error {
	q := s.DB.Queries()
	docs, err := s.sourceSpecDocs(ctx, q, src.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, d := range docs {
		to, ok := moved[d.ID]
		if moved != nil && !ok {
			continue
		}
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		ref.ReadOnly, ref.MovedTo = reason, to
		raw, _ := json.Marshal(ref)
		if err := q.SetSpecDocSourceRef(ctx, pgdb.SetSpecDocSourceRefParams{ID: d.ID, SourceRef: dbtype.JSON(raw), UpdatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// liftReadOnly lifts a read-only reason from the docs of a source: a reopened pull request
// is live again.
func (s *Service) liftReadOnly(ctx context.Context, id uuid.UUID, reason string) error {
	q := s.DB.Queries()
	docs, err := s.sourceSpecDocs(ctx, q, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		if ref.ReadOnly != reason {
			continue
		}
		ref.ReadOnly, ref.MovedTo = "", uuid.Nil
		raw, _ := json.Marshal(ref)
		if err := q.SetSpecDocSourceRef(ctx, pgdb.SetSpecDocSourceRefParams{ID: d.ID, SourceRef: dbtype.JSON(raw), UpdatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// mergePull runs when the pull request merges. A doc that another source already holds on the
// base branch becomes read-only, with a link to that doc: one doc never has two live bundles.
// The other docs stay live and follow the base branch from now on.
func (s *Service) mergePull(ctx context.Context, src pgdb.GithubSource) error {
	q := s.DB.Queries()
	held, err := s.heldElsewhere(ctx, q, src)
	if err != nil {
		return err
	}
	docs, err := s.sourceSpecDocs(ctx, q, src.ID)
	if err != nil {
		return err
	}
	moved := map[uuid.UUID]uuid.UUID{}
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		if h, ok := held[path.Join(ref.Dir, d.DocPath)]; ok {
			moved[d.ID] = h.doc
		}
	}
	if len(moved) == 0 {
		return nil
	}
	return s.freezePull(ctx, src, ReadOnlyMerged, moved)
}

// heldDoc is a spec doc that another source holds, and whether that source is a merged pull
// request source.
type heldDoc struct {
	doc  uuid.UUID
	pull bool
}

// heldElsewhere maps the repo path of each live, writable spec doc that another source of the
// same repo and branch holds to that spec doc.
func (s *Service) heldElsewhere(ctx context.Context, q store.Querier, src pgdb.GithubSource) (map[string]heldDoc, error) {
	srcs, err := q.ListGithubSources(ctx, s.Workspace)
	if err != nil {
		return nil, err
	}
	same := map[uuid.UUID]bool{}
	pull := map[uuid.UUID]bool{}
	for _, o := range srcs {
		if o.ID != src.ID && strings.EqualFold(o.Repo, src.Repo) && o.Branch == src.Branch && o.ApiUrl == src.ApiUrl &&
			(!isPull(o) || o.PullState == PullMerged) {
			same[o.ID], pull[o.ID] = true, isPull(o)
		}
	}
	out := map[string]heldDoc{}
	if len(same) == 0 {
		return out, nil
	}
	docs, err := q.ListSpecDocsBySource(ctx, pgdb.ListSpecDocsBySourceParams{WorkspaceID: s.Workspace, SourceKind: KindGitHub})
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		if same[ref.Source] && !d.ArchivedAt.Valid && ref.ReadOnly == "" {
			out[path.Join(ref.Dir, d.DocPath)] = heldDoc{doc: d.ID, pull: pull[ref.Source]}
		}
	}
	return out, nil
}

// pullAuthors makes the person who opened the pull request an author of its bundles, once
// their GitHub account is linked to a Speccy account.
func (s *Service) pullAuthors(ctx context.Context, src pgdb.GithubSource) error {
	if s.UserOfGitHub == nil || src.PullAuthorID == "" {
		return nil
	}
	user, ok, err := s.UserOfGitHub(ctx, src.PullAuthorID)
	if err != nil || !ok {
		return err
	}
	return s.addPullAuthor(ctx, src, user)
}

func (s *Service) addPullAuthor(ctx context.Context, src pgdb.GithubSource, user string) error {
	q := s.DB.Queries()
	docs, err := s.sourceSpecDocs(ctx, q, src.ID)
	if err != nil {
		return err
	}
	for _, d := range docs {
		if err := q.InsertBundleAuthor(ctx, pgdb.InsertBundleAuthorParams{BundleID: d.BundleID, UserID: user}); err != nil {
			return err
		}
	}
	return nil
}

// GitHubLinked runs when a person signs in with GitHub or links a GitHub account. The pull
// request sources of that account's pull requests make the person an author of their bundles.
func (s *Service) GitHubLinked(ctx context.Context, userID, githubID string) {
	srcs, err := s.DB.Queries().ListGithubSourcesOfAuthor(ctx, pgdb.ListGithubSourcesOfAuthorParams{WorkspaceID: s.Workspace, PullAuthorID: githubID})
	if err != nil {
		slog.WarnContext(ctx, "the pull request sources of a GitHub account were not listed", "err", err)
		return
	}
	for _, src := range srcs {
		if err := s.addPullAuthor(ctx, src, userID); err != nil {
			slog.WarnContext(ctx, "the author of a pull request source was not recorded", "source", src.ID, "err", err)
		}
	}
}

// syncReplies keeps the replies to Speccy's comments on the pull request: each thread whose
// first comment holds a key marker, on a file of a spec doc of the source.
func (s *Service) syncReplies(ctx context.Context, c *github.Client, src pgdb.GithubSource) error {
	q := s.DB.Queries()
	docs, err := s.sourceSpecDocs(ctx, q, src.ID)
	if err != nil || len(docs) == 0 {
		return err
	}
	threads, err := c.ReviewThreads(ctx, src.Repo, int(src.Pull))
	if err != nil {
		return err
	}
	// A comment sits on a file of a bundle: the spec doc of that bundle's folder.
	byDir := map[string]pgdb.SpecDoc{}
	byFile := map[string]pgdb.SpecDoc{}
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		byFile[path.Join(ref.Dir, d.DocPath)] = d
		byDir[ref.Dir] = d
	}
	for _, t := range threads {
		k := findingkey.In(t.Body)
		if k == "" || len(t.Replies) == 0 {
			continue
		}
		d, ok := byFile[t.Path]
		if !ok {
			d, ok = byDir[path.Dir(t.Path)]
		}
		if !ok {
			continue
		}
		for _, r := range t.Replies {
			if r.ID == 0 || strings.HasSuffix(r.Author, "[bot]") {
				continue
			}
			if err := q.UpsertGithubReply(ctx, pgdb.UpsertGithubReplyParams{SourceID: src.ID, CommentID: r.ID, SpecDocID: d.ID,
				FindingKey: k, Author: r.Author, Body: r.Body, Url: r.URL, CreatedAt: r.At.UTC()}); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncPull syncs the sources of one pull request, after GitHub said that it changed: a push, a
// close, a merge, or a reply to a comment.
func (s *Service) SyncPull(ctx context.Context, repo string, pull int) {
	srcs, err := s.DB.Queries().ListGithubSourcesOfPull(ctx, pgdb.ListGithubSourcesOfPullParams{WorkspaceID: s.Workspace, Repo: repo, Pull: int64(pull)})
	if err != nil {
		slog.WarnContext(ctx, "a GitHub webhook could not list the sources of a pull request", "repo", repo, "pull", pull, "err", err)
		return
	}
	for _, src := range srcs {
		if err := s.SyncSource(ctx, src.ID, false); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "sync of a pull request source after a webhook failed", "repo", src.Repo, "pull", src.Pull, "err", err)
		}
	}
}

// PullDocs are the live spec docs of the pull request source of repo and pull, with the folder
// of each in the repo. The newest source wins when two follow the same pull request.
func (s *Service) PullDocs(ctx context.Context, repo string, pull int) (pgdb.GithubSource, []pgdb.SpecDoc, map[uuid.UUID]string, error) {
	q := s.DB.Queries()
	srcs, err := q.ListGithubSourcesOfPull(ctx, pgdb.ListGithubSourcesOfPullParams{WorkspaceID: s.Workspace, Repo: repo, Pull: int64(pull)})
	if err != nil {
		return pgdb.GithubSource{}, nil, nil, err
	}
	if len(srcs) == 0 {
		return pgdb.GithubSource{}, nil, nil, nil
	}
	src := srcs[len(srcs)-1]
	docs, err := s.sourceSpecDocs(ctx, q, src.ID)
	if err != nil {
		return src, nil, nil, err
	}
	dirs := map[uuid.UUID]string{}
	for _, d := range docs {
		var ref githubRef
		_ = json.Unmarshal(d.SourceRef, &ref)
		dirs[d.ID] = ref.Dir
	}
	return src, docs, dirs, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
