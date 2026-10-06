package review

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/alternayte/speccy/internal/kernel"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
	"github.com/alternayte/speccy/internal/source/local"
)

// MaxURLDocs is the most spec docs that one review of a URL takes. Each doc is a review of its
// own, with its model calls.
const MaxURLDocs = 20

// URLReview is the review of the spec docs that a GitHub URL names, at one commit. Nothing of
// it is a saved bundle (#92).
type URLReview struct {
	Repo   string
	Commit string
	// Pull is the number of the pull request, for a pull request URL.
	Pull int
	Docs []URLDoc
	// Config is the .speccy.yaml that the review used.
	Config URLConfig
}

// URLConfig is the .speccy.yaml that a review of a URL used: the repo's own at the commit, or,
// when the repo has none, the one of the folder that this Speccy serves (#131).
type URLConfig struct {
	Source string // ConfigRepo, ConfigLocal or ConfigNone
	// Path is the file: in the repo, or on this machine.
	Path string
	PR   source.PRConfig
}

// The places a review of a URL takes its .speccy.yaml from.
const (
	ConfigRepo  = "repo"
	ConfigLocal = "local"
	ConfigNone  = "none"
)

// URLDoc is one reviewed spec doc: where it is in the repo, its files, and its result, or the
// reason the review of this doc stopped.
type URLDoc struct {
	Slug string
	// Dir is the folder of the doc's bundle and Path the doc, both relative to the repo root.
	Dir    string
	Path   string
	Files  []source.File
	Result ContentResult
	Err    error
}

// ReviewURL reviews the spec docs of a GitHub file, folder, branch, commit or pull request. It
// reads the files at the head commit with the GitHub credential that Speccy holds. For a pull
// request it takes the spec docs that the pull request changes; for a folder, the spec docs
// under it. The review uses the repo's own .speccy.yaml and sidecars at that commit, and a
// link from one doc resolves to another spec doc of the same commit. The docs review in
// parallel, URLParallel at a time.
func (s *Service) ReviewURL(ctx context.Context, raw string, stages Stages) (URLReview, error) {
	plan, err := s.PlanURL(ctx, raw)
	if err != nil {
		return plan.Review, err
	}
	return s.ReviewPlan(ctx, plan, stages, make(chan struct{}, URLParallel)), nil
}

// URLParallel is how many spec docs of one URL review at the same time.
const URLParallel = 3

// URLPlan is the spec docs of a URL, read from the repo, before any model call.
type URLPlan struct {
	// Review holds the repo, the commit and the pull request, and no doc yet.
	Review URLReview
	Items  []URLItem
}

// URLItem is one spec doc of a plan: its place and files, and the content to review, or the
// reason it does not load.
type URLItem struct {
	Doc     URLDoc
	Content Content
}

// ReviewPlan reviews each doc of the plan. A doc takes a slot of slots for its review, so one
// set of slots limits the reviews of many plans together. The docs keep their order.
func (s *Service) ReviewPlan(ctx context.Context, plan URLPlan, stages Stages, slots chan struct{}) URLReview {
	out := plan.Review
	out.Docs = make([]URLDoc, len(plan.Items))
	var wg sync.WaitGroup
	for i, it := range plan.Items {
		out.Docs[i] = it.Doc
		if it.Doc.Err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				out.Docs[i].Err = ctx.Err()
				return
			}
			defer func() { <-slots }()
			out.Docs[i].Result, out.Docs[i].Err = s.ReviewContent(ctx, it.Content, stages)
		}()
	}
	wg.Wait()
	return out
}

// EstimatePlan estimates the chosen stages on each doc of the plan that loads.
func (s *Service) EstimatePlan(ctx context.Context, plan URLPlan, stages Stages) (Estimate, error) {
	var sum Estimate
	for _, it := range plan.Items {
		if it.Doc.Err != nil {
			continue
		}
		est, err := s.EstimateContent(ctx, it.Content, stages)
		if err != nil {
			return sum, err
		}
		sum.Calls += est.Calls
		sum.CachedHits += est.CachedHits
		sum.TokensIn += est.TokensIn
		sum.TokensOut += est.TokensOut
		sum.CostUSD += est.CostUSD
		sum.Priced = sum.Priced || est.Priced
	}
	return sum, nil
}

// PlanURL reads the spec docs of a GitHub URL and what each one needs for its review, with no
// model call.
func (s *Service) PlanURL(ctx context.Context, raw string) (URLPlan, error) {
	var plan URLPlan
	out := &plan.Review
	build, err := github.ParseBuildURL(raw)
	if err != nil {
		return plan, kernel.Invalid("bad_url", "%s.", err.Error())
	}
	if s.GitHub == nil {
		return plan, kernel.Invalid("no_github", "This Speccy has no GitHub credential, so it cannot read %s.", raw)
	}
	c, err := s.GitHub(ctx, build.APIURL())
	if err != nil {
		return plan, err
	}
	out.Repo, out.Pull = build.Repo, build.Pull
	ref := build.SHA
	var changed []string
	switch {
	case build.Pull > 0:
		pr, err := c.PullRequest(ctx, build.Repo, build.Pull)
		if err != nil {
			return plan, github.UnreadableRepo(build.Repo, err)
		}
		files, err := c.PRFiles(ctx, build.Repo, build.Pull)
		if err != nil {
			return plan, github.UnreadableRepo(build.Repo, err)
		}
		ref = pr.HeadSHA
		for _, f := range files {
			if f.Status != "removed" {
				changed = append(changed, f.Filename)
			}
		}
	case ref == "":
		resolved, err := c.ResolveBranch(ctx, build.Ref)
		if err != nil {
			return plan, github.UnreadableRepo(build.Repo, err)
		}
		build.Ref = resolved
		if ref = build.Branch; ref == "" {
			if ref, err = c.Repo(ctx, build.Repo); err != nil {
				return plan, github.UnreadableRepo(build.Repo, err)
			}
		}
	}
	commit, tree, err := c.CommitTree(ctx, build.Repo, ref)
	if err != nil {
		return plan, github.UnreadableRepo(build.Repo, err)
	}
	out.Commit = commit
	entries, truncated, err := c.Tree(ctx, build.Repo, tree)
	if err != nil {
		return plan, github.UnreadableRepo(build.Repo, err)
	}
	if truncated {
		return plan, kernel.Invalid("tree_too_large", "The tree of %s is too large for the GitHub API.", build.Repo)
	}
	// Speccy reads a blob only when the scan or a review opens the file, and only near the
	// docs that the URL names. A blob is read once.
	blobs := map[string][]byte{}
	load := func(e github.Entry) ([]byte, error) {
		if b, ok := blobs[e.SHA]; ok {
			return b, nil
		}
		b, err := c.Blob(ctx, build.Repo, e.SHA)
		if err == nil {
			blobs[e.SHA] = b
		}
		return b, err
	}
	near := nearPaths(build, changed)
	var extra []string // the folders of the docs that the reviewed docs link to
	keep := func(p string) bool {
		if p == source.RepoConfigFile || source.IsSidecar(p) || near(p) {
			return true
		}
		for _, dir := range extra {
			if github.Under(p, dir) {
				return true
			}
		}
		return false
	}
	tfs := github.NewTreeFS(entries, keep, load)
	// The repo's own .speccy.yaml wins. A repo with none takes the .speccy.yaml of the served
	// folder, so a reviewer can review a repo that they cannot change (#131).
	cfg := source.RepoConfig{}
	out.Config = URLConfig{Source: ConfigNone}
	rawCfg, err := fs.ReadFile(tfs, source.RepoConfigFile)
	switch {
	case err == nil:
		if cfg, err = source.ParseRepoConfig(rawCfg); err != nil {
			return plan, kernel.Invalid("bad_repo_config", "%s in the repo is not valid: %s.", source.RepoConfigFile, err.Error())
		}
		out.Config = URLConfig{Source: ConfigRepo, Path: source.RepoConfigFile}
	case !errors.Is(err, fs.ErrNotExist):
		return plan, github.UnreadableRepo(build.Repo, err)
	case s.LocalConfig != nil:
		local, file, found, err := s.LocalConfig()
		if err != nil {
			return plan, kernel.Invalid("bad_local_config", "%s is not valid: %s.", file, err.Error())
		}
		if found {
			cfg, out.Config = local, URLConfig{Source: ConfigLocal, Path: file}
		}
	}
	out.Config.PR = cfg.PR
	var root *local.Root
	var scan *local.Scan
	var selected []local.Bundle
	// The first scan finds the docs that the URL names. A doc can link to a doc in another
	// folder of the repo, so a second scan also reads the folders that those links name.
	for pass := 0; pass < 2; pass++ {
		// A bundle carries the files its doc references above its folder, from the whole repo.
		root = local.FromFS(github.NewTreeFS(entries, keep, load)).WithRepo(github.NewTreeFS(entries, func(string) bool { return true }, load))
		if scan, err = root.Scan(cfg); err != nil {
			return plan, err
		}
		selected = selected[:0]
		for _, b := range scan.Bundles {
			if namedBy(b, build, changed) {
				selected = append(selected, b)
			}
		}
		before := len(extra)
		for _, b := range selected {
			extra = append(extra, linkedFolders(b, cfg)...)
		}
		if len(extra) == before || len(selected) > MaxURLDocs {
			break
		}
	}
	// A doc that names no type and that no mapping covers is still reviewable when the URL
	// names that one file: the review picks its profile (REQ-135).
	if len(selected) == 0 && build.Pull == 0 && build.File {
		content, err := fs.ReadFile(tfs, build.Path)
		if err != nil {
			return plan, kernel.NotFound("no_such_file", "%s has no file %s at %s.", build.Repo, build.Path, short(commit))
		}
		file := path.Base(build.Path)
		main, err := source.SingleFileMainDoc(file, content, "")
		if err != nil {
			return plan, kernel.Invalid("bad_main_doc", "%s has %s.", build.Path, err.Error())
		}
		selected = []local.Bundle{{Slug: strings.TrimSuffix(build.Path, path.Ext(build.Path)), Dir: path.Dir(build.Path), File: file,
			Main: main, Files: []source.File{{Path: file, Content: content}}, Unnamed: true}}
	}
	if len(selected) == 0 {
		return plan, kernel.Invalid("no_spec_doc", "%s names no spec doc at %s. A spec doc is a markdown file with a type in its frontmatter, or a file that a map entry in %s selects.",
			raw, short(commit), source.RepoConfigFile)
	}
	if len(selected) > MaxURLDocs {
		return plan, kernel.Invalid("too_many_docs", "%s names %d spec docs, and one review takes %d at most. Name a folder or a doc.", raw, len(selected), MaxURLDocs)
	}
	// A link rule target that the commit does not hold can be in an open pull request of the
	// repo (#142). The docs of this review share one finder, so it lists them once.
	inTree := map[string]bool{}
	for _, e := range entries {
		if e.Type == "blob" {
			inTree[e.Path] = true
		}
	}
	pulls := newPullFinder(clientOf(c), build.Repo, build.Pull, func(_ context.Context, _ *github.Client, p string) (bool, error) { return inTree[p], nil })
	// Every spec doc of the scan can be the target of a link, so each one loads its files once.
	loaded := map[string][]source.File{}
	filesOf := func(b local.Bundle) ([]source.File, error) {
		if b.Unnamed {
			return b.Files, nil
		}
		if f, ok := loaded[b.Slug]; ok {
			return f, nil
		}
		f, err := root.Load(scan, b.Slug)
		loaded[b.Slug] = f
		return f, err
	}
	for _, b := range selected {
		doc := URLDoc{Slug: b.Slug, Dir: b.Dir, Path: path.Join(b.Dir, b.Main.Path)}
		if doc.Files, doc.Err = filesOf(b); doc.Err != nil {
			plan.Items = append(plan.Items, URLItem{Doc: doc})
			continue
		}
		from := &FromRepo{Dir: b.Dir, Config: cfg, Refs: b.Refs, pulls: pulls, Repo: build.Repo}
		if rawDec, err := fs.ReadFile(tfs, source.SidecarPath(doc.Path)); err == nil {
			if from.Decisions, err = source.ParseDecisions(rawDec); err != nil {
				doc.Err = kernel.Invalid("bad_sidecar", "The sidecar of %s is not valid: %s.", doc.Path, err.Error())
				plan.Items = append(plan.Items, URLItem{Doc: doc})
				continue
			}
		}
		for _, o := range scan.Bundles {
			if o.Slug == b.Slug {
				continue
			}
			files, err := filesOf(o)
			if err != nil {
				continue // a doc that does not load is no link target
			}
			sib := Sibling{Slug: o.Slug, Dir: o.Dir, DocPath: o.Main.Path, Profile: o.Main.Frontmatter.Type, Title: o.Main.Title, Files: files}
			if raw, err := fs.ReadFile(tfs, source.SidecarPath(path.Join(o.Dir, o.Main.Path))); err == nil {
				sib.Decisions, _ = source.ParseDecisions(raw) // a sidecar that does not parse asks nothing
			}
			from.Siblings = append(from.Siblings, sib)
		}
		content := Content{Slug: b.Slug, Files: doc.Files, From: from}
		if b.File != "" {
			content.MainDoc, content.Profile = b.File, b.Main.Frontmatter.Type
		}
		plan.Items = append(plan.Items, URLItem{Doc: doc, Content: content})
	}
	return plan, nil
}

// linkedFolders returns the folders where the docs that b links to can be: the folder of each
// link target that is a path or a slug, relative to the doc and to the repo root, and the
// folder of each target that a link rule of the repo gives.
func linkedFolders(b local.Bundle, cfg source.RepoConfig) []string {
	var out []string
	add := func(target string) {
		target = strings.Trim(path.Clean(target), "/")
		if target == "" || target == "." || strings.HasPrefix(target, "..") {
			return
		}
		// A target is a doc or a folder: read the folder that holds it, and the target itself.
		out = append(out, target)
		if dir := path.Dir(target); dir != "." {
			out = append(out, dir)
		}
	}
	for _, l := range b.Main.Frontmatter.Links {
		target := strings.TrimSpace(l.Target)
		if target == "" || source.IsExternalTarget(target) {
			continue
		}
		add(path.Join(b.Dir, target))
		add(target)
	}
	for _, rule := range linkRules(cfg) {
		if to, ok := rule.Target(path.Join(b.Dir, b.Main.Path)); ok {
			add(to)
		}
	}
	return out
}

// namedBy reports whether the URL names the bundle: a pull request names the bundles whose
// files it changes, and a path names the bundles at it or under it.
func namedBy(b local.Bundle, build github.Build, changed []string) bool {
	if build.Pull > 0 {
		for _, f := range changed {
			if b.Names(f, false) {
				return true
			}
		}
		return false
	}
	return b.Names(build.Path, !build.File)
}

// nearPaths returns which files of the tree a review of the URL may read: for a path, the
// files at it and under it; for a pull request, the folder of each changed file, and the files
// directly in each folder above it, where the main doc of a changed asset is.
func nearPaths(build github.Build, changed []string) func(p string) bool {
	if build.Pull == 0 {
		dir := build.Path
		if build.File {
			dir = path.Dir(dir)
		}
		return func(p string) bool { return dir == "." || github.Under(p, dir) }
	}
	under, direct := map[string]bool{}, map[string]bool{}
	for _, f := range changed {
		d := path.Dir(f)
		under[d] = true
		for d != "." && d != "/" {
			d = path.Dir(d)
			direct[d] = true
		}
	}
	return func(p string) bool {
		if direct[path.Dir(p)] {
			return true
		}
		for d := path.Dir(p); ; d = path.Dir(d) {
			if under[d] {
				return true
			}
			if d == "." || d == "/" {
				return false
			}
		}
	}
}
