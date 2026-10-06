package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/db/dbtype"
	pgdb "github.com/alternayte/speccy/db/postgres"
	"github.com/alternayte/speccy/internal/engine/verdict"
	"github.com/alternayte/speccy/internal/source"
	"github.com/alternayte/speccy/internal/source/github"
)

// An upstream doc that a link rule names can exist only in an open pull request of the same
// repo, while its own review runs (#142). A review that knows its GitHub repo reads that doc at
// the head commit of the pull request. A doc in the tree always wins.

// pullRef is the open pull request that a link target comes from.
type pullRef struct {
	Number int
	SHA    string
	URL    string
}

// FolderRepo is the GitHub repo that the served folder is a checkout of. Only the CI Action
// sets it: a folder on a laptop names no repo that Speccy can trust.
type FolderRepo struct {
	Client *github.Client
	Repo   string
	// Dir is the checkout on disk, and Pull the pull request that the Action reviews, or 0.
	Dir  string
	Pull int
}

// pulledDoc is a doc that an open pull request adds or changes, at its head commit. others
// are the other open pull requests that change the same path.
type pulledDoc struct {
	pull    pullRef
	others  []int
	content []byte
}

// pullFinder finds the docs that the open pull requests of one repo add or change. It lists
// the open pull requests once, and reads the files of a pull request once, only when a link
// target is not in the tree.
type pullFinder struct {
	// connect returns the client of the repo. The finder calls it only when a target is
	// missing, because a client can cost a call to gh.
	connect func(ctx context.Context) (*github.Client, error)
	repo    string
	// skip is the pull request under review: its head is the tree itself.
	skip int
	// inTree reports whether the tree that the review reads holds the path.
	inTree func(ctx context.Context, c *github.Client, p string) (bool, error)

	mu        sync.Mutex
	client    *github.Client
	listed    bool
	pulls     []github.Pull
	listErr   error
	files     map[int][]github.PRFile
	found     map[string]*pulledDoc
	connected bool
	connErr   error
}

func newPullFinder(connect func(context.Context) (*github.Client, error), repo string, skip int,
	inTree func(context.Context, *github.Client, string) (bool, error)) *pullFinder {
	return &pullFinder{connect: connect, repo: repo, skip: skip, inTree: inTree, files: map[int][]github.PRFile{}, found: map[string]*pulledDoc{}}
}

// clientOf returns a connect function for a client that a caller already holds.
func clientOf(c *github.Client) func(context.Context) (*github.Client, error) {
	return func(context.Context) (*github.Client, error) { return c, nil }
}

// find returns the doc at p in the open pull request with the latest update that adds or
// changes exactly p, or nil when the tree holds p or no open pull request changes it.
func (f *pullFinder) find(ctx context.Context, p string) (*pulledDoc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.found[p]; ok {
		return d, nil
	}
	d, err := f.lookup(ctx, p)
	if err != nil {
		return nil, err
	}
	f.found[p] = d
	return d, nil
}

func (f *pullFinder) lookup(ctx context.Context, p string) (*pulledDoc, error) {
	if !f.connected {
		f.client, f.connErr = f.connect(ctx)
		f.connected = true
	}
	if f.connErr != nil {
		return nil, f.connErr
	}
	if f.inTree != nil {
		held, err := f.inTree(ctx, f.client, p)
		if err != nil {
			return nil, err
		}
		if held {
			return nil, nil
		}
	}
	if !f.listed {
		f.pulls, f.listErr = f.client.OpenPulls(ctx, f.repo)
		f.listed = true
		// The pull request with the latest update comes first.
		slices.SortStableFunc(f.pulls, func(a, b github.Pull) int {
			if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
				return c
			}
			return b.Number - a.Number
		})
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	var matches []github.Pull
	for _, pl := range f.pulls {
		if pl.Number == f.skip {
			continue
		}
		files, ok := f.files[pl.Number]
		if !ok {
			var err error
			if files, err = f.client.PRFiles(ctx, f.repo, pl.Number); err != nil {
				return nil, err
			}
			f.files[pl.Number] = files
		}
		for _, file := range files {
			if file.Filename == p && file.Status != "removed" {
				matches = append(matches, pl)
				break
			}
		}
	}
	for i, m := range matches {
		content, ok, err := f.client.FileAt(ctx, f.repo, m.HeadSHA, p)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		d := &pulledDoc{pull: pullRef{Number: m.Number, SHA: m.HeadSHA, URL: m.URL}, content: content}
		for j, o := range matches {
			if j != i {
				d.others = append(d.others, o.Number)
			}
		}
		return d, nil
	}
	return nil, nil
}

// pullsKey holds the pull finders of one review in a context.
type pullsKey struct{}

// pullFinders are the pull finders of one review, one for each repo.
type pullFinders struct {
	mu sync.Mutex
	m  map[string]*pullFinder
}

// withPulls marks ctx as a review that may read open pull requests. A review that already
// has its finders keeps them, so a pass over many docs lists the pull requests of a repo once.
func withPulls(ctx context.Context) context.Context {
	if ctx.Value(pullsKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, pullsKey{}, &pullFinders{m: map[string]*pullFinder{}})
}

func (fs *pullFinders) get(key string, make func() *pullFinder) *pullFinder {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	f, ok := fs.m[key]
	if !ok {
		f = make()
		fs.m[key] = f
	}
	return f
}

// pullFinderFor returns the finder of the repo of b, or nil when the review must not read
// open pull requests: content with no repo, a doc in no GitHub repo, or a read that is not a
// review.
func (s *Service) pullFinderFor(ctx context.Context, b pgdb.SpecDoc, from *FromRepo, held githubDocs) *pullFinder {
	if from != nil {
		return from.pulls
	}
	fs, _ := ctx.Value(pullsKey{}).(*pullFinders)
	if fs == nil {
		return nil
	}
	var r bundleRef
	_ = json.Unmarshal(b.SourceRef, &r)
	if r.Source != uuid.Nil {
		src, ok := held[r.Source]
		if !ok || s.GitHub == nil {
			return nil
		}
		return fs.get("source|"+src.ID.String(), func() *pullFinder {
			connect := func(ctx context.Context) (*github.Client, error) { return s.GitHub(ctx, src.ApiUrl) }
			return newPullFinder(connect, src.Repo, 0, func(ctx context.Context, c *github.Client, p string) (bool, error) {
				ref := src.HeadCommit
				if ref == "" {
					ref = src.Branch
				}
				_, ok, err := c.FileAt(ctx, src.Repo, ref, p)
				return ok, err
			})
		})
	}
	if f := s.FolderRepo; f != nil && b.SourceKind == "local" {
		return fs.get("folder", func() *pullFinder {
			return newPullFinder(clientOf(f.Client), f.Repo, f.Pull, func(_ context.Context, _ *github.Client, p string) (bool, error) {
				_, err := os.Stat(filepath.Join(f.Dir, filepath.FromSlash(p)))
				if os.IsNotExist(err) {
					return false, nil
				}
				return err == nil, err
			})
		})
	}
	return nil
}

// pullTargets finds, in the open pull requests of the repo, the target of each link rule of b
// that names no spec doc in all. It returns the spec docs it found by their path, with the
// run notes about them.
func (s *Service) pullTargets(ctx context.Context, f *pullFinder, all []pgdb.SpecDoc, place docPlace, b pgdb.SpecDoc,
	rules []source.LinkRule, repo source.RepoConfig) (map[string]*pulledTarget, []string) {
	if f == nil {
		return nil, nil
	}
	from := bundlePath(b, place)
	var out map[string]*pulledTarget
	var notes []string
	for _, rule := range rules {
		to, ok := rule.Target(from)
		if !ok || to == from || out[to] != nil {
			continue
		}
		if slices.ContainsFunc(all, func(d pgdb.SpecDoc) bool { return !d.ArchivedAt.Valid && bundlePath(d, place) == to }) {
			continue // a doc in the tree wins
		}
		d, err := f.find(ctx, to)
		if err != nil {
			notes = append(notes, fmt.Sprintf("Speccy could not read the open pull requests of %s, so the %s link to %s has no target. %s",
				f.repo, rule.Kind, to, sentence(err.Error())))
			continue
		}
		if d == nil {
			continue
		}
		t := pulledDocTarget(f.repo, to, d, repo)
		if t == nil {
			continue // the file names no type and no map entry covers it: it is no spec doc
		}
		if out == nil {
			out = map[string]*pulledTarget{}
		}
		out[to] = t
		if len(d.others) > 0 {
			nums := make([]string, len(d.others))
			for i, n := range d.others {
				nums[i] = fmt.Sprintf("#%d", n)
			}
			notes = append(notes, fmt.Sprintf("Other open pull requests also change %s: %s. The review used #%d, the one with the latest update.",
				to, strings.Join(nums, ", "), d.pull.Number))
		}
	}
	return out, notes
}

// pulledTarget is a spec doc that an open pull request holds, as the link resolver reads it.
type pulledTarget struct {
	doc   pgdb.SpecDoc
	pull  pullRef
	files []source.File
}

// pulledDocTarget makes the spec doc row of a doc from a pull request. Its ID is the same for
// the same doc at the same commit, so the model cache holds across reviews.
func pulledDocTarget(repoName, p string, d *pulledDoc, repo source.RepoConfig) *pulledTarget {
	mapped, _ := repo.MappedProfile(p)
	main, err := source.SingleFileMainDoc(path.Base(p), d.content, mapped)
	if err != nil || main.Frontmatter.Type == "" {
		return nil
	}
	ref, _ := json.Marshal(bundleRef{Dir: path.Dir(p)})
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("speccy:pull:%s:%d:%s:%s", repoName, d.pull.Number, d.pull.SHA, p)))
	doc := pgdb.SpecDoc{ID: id, Slug: strings.TrimSuffix(p, path.Ext(p)), Title: main.Title, ProfileKey: main.Frontmatter.Type,
		DocPath: path.Base(p), SourceRef: dbtype.JSON(ref)}
	return &pulledTarget{doc: doc, pull: d.pull, files: []source.File{{Path: doc.DocPath, Content: d.content}}}
}

// pullNotes are the run notes of the links whose target comes from an open pull request.
func pullNotes(links []link) []string {
	var out []string
	for _, l := range links {
		if l.pull != nil {
			out = append(out, fmt.Sprintf("The %s link to %s reads the doc from pull request #%d at %s. That doc is not merged.",
				l.kind, l.ref, l.pull.Number, short(l.pull.SHA)))
		}
	}
	return out
}

// readyOnPullNote is the run note of a Build Ready verdict that depends on a doc that is not
// merged, or "".
func readyOnPullNote(links []link, result verdict.Result) string {
	if result != verdict.BuildReady {
		return ""
	}
	var docs []string
	for _, l := range links {
		if l.pull != nil {
			docs = append(docs, fmt.Sprintf("%s (pull request #%d)", l.ref, l.pull.Number))
		}
	}
	if len(docs) == 0 {
		return ""
	}
	return "This Build Ready verdict depends on an upstream doc that is not merged: " + strings.Join(docs, ", ") +
		". Review this doc again after that pull request merges or changes."
}

// appendNote adds a note to the JSON list of run notes.
func appendNote(notes dbtype.JSON, note string) dbtype.JSON {
	var list []string
	_ = json.Unmarshal(notes, &list)
	out, _ := json.Marshal(append(list, note))
	return out
}
