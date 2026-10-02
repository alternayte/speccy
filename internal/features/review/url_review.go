package review

import (
	"context"
	"io/fs"
	"path"
	"strings"

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
}

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
// link from one doc resolves to another spec doc of the same commit.
func (s *Service) ReviewURL(ctx context.Context, raw string, stages Stages) (URLReview, error) {
	var out URLReview
	build, err := github.ParseBuildURL(raw)
	if err != nil {
		return out, kernel.Invalid("bad_url", "%s.", err.Error())
	}
	if s.GitHub == nil {
		return out, kernel.Invalid("no_github", "This Speccy has no GitHub credential, so it cannot read %s.", raw)
	}
	c, err := s.GitHub(ctx, build.APIURL())
	if err != nil {
		return out, err
	}
	out.Repo, out.Pull = build.Repo, build.Pull
	ref := build.SHA
	var changed []string
	switch {
	case build.Pull > 0:
		pr, err := c.PullRequest(ctx, build.Repo, build.Pull)
		if err != nil {
			return out, github.UnreadableRepo(build.Repo, err)
		}
		files, err := c.PRFiles(ctx, build.Repo, build.Pull)
		if err != nil {
			return out, github.UnreadableRepo(build.Repo, err)
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
			return out, github.UnreadableRepo(build.Repo, err)
		}
		build.Ref = resolved
		if ref = build.Branch; ref == "" {
			if ref, err = c.Repo(ctx, build.Repo); err != nil {
				return out, github.UnreadableRepo(build.Repo, err)
			}
		}
	}
	commit, tree, err := c.CommitTree(ctx, build.Repo, ref)
	if err != nil {
		return out, github.UnreadableRepo(build.Repo, err)
	}
	out.Commit = commit
	entries, truncated, err := c.Tree(ctx, build.Repo, tree)
	if err != nil {
		return out, github.UnreadableRepo(build.Repo, err)
	}
	if truncated {
		return out, kernel.Invalid("tree_too_large", "The tree of %s is too large for the GitHub API.", build.Repo)
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
	cfg := source.RepoConfig{}
	if rawCfg, err := fs.ReadFile(tfs, source.RepoConfigFile); err == nil {
		if cfg, err = source.ParseRepoConfig(rawCfg); err != nil {
			return out, kernel.Invalid("bad_repo_config", "%s in the repo is not valid: %s.", source.RepoConfigFile, err.Error())
		}
	}
	var root *local.Root
	var scan *local.Scan
	var selected []local.Bundle
	// The first scan finds the docs that the URL names. A doc can link to a doc in another
	// folder of the repo, so a second scan also reads the folders that those links name.
	for pass := 0; pass < 2; pass++ {
		root = local.FromFS(github.NewTreeFS(entries, keep, load))
		if scan, err = root.Scan(cfg); err != nil {
			return out, err
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
			return out, kernel.NotFound("no_such_file", "%s has no file %s at %s.", build.Repo, build.Path, short(commit))
		}
		file := path.Base(build.Path)
		main, err := source.SingleFileMainDoc(file, content, "")
		if err != nil {
			return out, kernel.Invalid("bad_main_doc", "%s has %s.", build.Path, err.Error())
		}
		selected = []local.Bundle{{Slug: strings.TrimSuffix(build.Path, path.Ext(build.Path)), Dir: path.Dir(build.Path), File: file,
			Main: main, Files: []source.File{{Path: file, Content: content}}, Unnamed: true}}
	}
	if len(selected) == 0 {
		return out, kernel.Invalid("no_spec_doc", "%s names no spec doc at %s. A spec doc is a markdown file with a type in its frontmatter, or a file that a map entry in %s selects.",
			raw, short(commit), source.RepoConfigFile)
	}
	if len(selected) > MaxURLDocs {
		return out, kernel.Invalid("too_many_docs", "%s names %d spec docs, and one review takes %d at most. Name a folder or a doc.", raw, len(selected), MaxURLDocs)
	}
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
			out.Docs = append(out.Docs, doc)
			continue
		}
		from := &FromRepo{Dir: b.Dir, Config: cfg}
		if rawDec, err := fs.ReadFile(tfs, source.SidecarPath(doc.Path)); err == nil {
			if from.Decisions, err = source.ParseDecisions(rawDec); err != nil {
				doc.Err = kernel.Invalid("bad_sidecar", "The sidecar of %s is not valid: %s.", doc.Path, err.Error())
				out.Docs = append(out.Docs, doc)
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
			from.Siblings = append(from.Siblings, Sibling{Slug: o.Slug, Dir: o.Dir, DocPath: o.Main.Path, Profile: o.Main.Frontmatter.Type,
				Title: o.Main.Title, Files: files})
		}
		content := Content{Slug: b.Slug, Files: doc.Files, From: from}
		if b.File != "" {
			content.MainDoc, content.Profile = b.File, b.Main.Frontmatter.Type
		}
		doc.Result, doc.Err = s.ReviewContent(ctx, content, stages)
		out.Docs = append(out.Docs, doc)
	}
	return out, nil
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
