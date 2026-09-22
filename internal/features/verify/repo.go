package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/features/profile"
	"github.com/alternayte/speccy/internal/source/github"
)

// Repo is the code the gate reads. It reads the whole tree at one commit, because a
// requirement is often satisfied by code that predates the build, and a diff-scoped search
// reports that code as missing.
type Repo interface {
	// Name is the repo, as the run records it.
	Name() string
	// SHA is the commit the run read, or an empty string for a folder.
	SHA() string
	// Digest identifies the content a folder run read.
	Digest() string
	// Files are the paths the scan covers, after the profile's limits.
	Files() []string
	// Read returns one file. ok is false when Speccy did not read it.
	Read(ctx context.Context, path string) ([]byte, bool, error)
	// Changed returns the paths the commits since base changed. It is nil when there is no
	// base, and the candidates then carry no ranking.
	Changed(ctx context.Context, base string) (map[string]bool, error)
	// Notes are the limits the scan hit, for the run report.
	Notes() []string
}

// scan applies the profile's bounds to a list of files, and says which limits it hit.
type scan struct {
	limits profile.Verify
	notes  []string
}

func (s *scan) keep(path string, size int64, binary bool) bool {
	for _, pat := range s.limits.Exclude {
		if matchGlob(pat, path) {
			s.note(fmt.Sprintf("The scan passed over %s, because the profile excludes %s.", path, pat))
			return false
		}
	}
	if max := int64(s.limits.MaxFileKB) << 10; size > max {
		s.note(fmt.Sprintf("The scan passed over %s, because it is larger than the %d kB limit.", path, s.limits.MaxFileKB))
		return false
	}
	if binary {
		s.note(fmt.Sprintf("The scan passed over %s, because it is not text.", path))
		return false
	}
	return true
}

// note keeps one line per limit kind, so a repo with a thousand excluded files gives a run
// report a person can read.
func (s *scan) note(line string) {
	kind := line
	if i := strings.Index(line, ", because"); i > 0 {
		kind = line[i:]
	}
	for _, n := range s.notes {
		if strings.HasSuffix(n, kind) {
			return
		}
	}
	s.notes = append(s.notes, line)
}

// matchGlob matches a path against a glob with ** for any number of segments.
func matchGlob(pattern, path string) bool {
	return globParts(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globParts(pat, seg []string) bool {
	for i, p := range pat {
		if p == "**" {
			if i == len(pat)-1 {
				return true
			}
			for j := i; j <= len(seg); j++ {
				if globParts(pat[i+1:], seg[j:]) {
					return true
				}
			}
			return false
		}
		if i >= len(seg) {
			return false
		}
		if ok, err := filepath.Match(p, seg[i]); err != nil || !ok {
			return false
		}
	}
	return len(pat) == len(seg)
}

// isBinary reports whether content is not text. A NUL byte in the first block is the test
// git itself uses.
func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	for i := range n {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// GitHubRepo reads a repo at one commit through the credential Speccy already holds.
type GitHubRepo struct {
	client *github.Client
	repo   string
	sha    string
	blobs  map[string]string
	files  []string
	notes  []string
	cache  map[string][]byte
}

// NewGitHubRepo lists the tree at sha and applies the profile's scan bounds.
func NewGitHubRepo(ctx context.Context, c *github.Client, repo, sha string, limits profile.Verify) (*GitHubRepo, error) {
	entries, truncated, err := c.Tree(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	s := &scan{limits: limits}
	if truncated {
		s.notes = append(s.notes, "GitHub cut the tree of this repo, so the scan did not cover every file.")
	}
	r := &GitHubRepo{client: c, repo: repo, sha: sha, blobs: map[string]string{}, cache: map[string][]byte{}}
	for _, e := range entries {
		if e.Type != "blob" {
			continue
		}
		// A tree entry carries no content, so the binary test happens on read.
		if !s.keep(e.Path, e.Size, false) {
			continue
		}
		r.blobs[e.Path] = e.SHA
		r.files = append(r.files, e.Path)
	}
	sort.Strings(r.files)
	r.notes = s.notes
	return r, nil
}

func (r *GitHubRepo) Name() string    { return r.repo }
func (r *GitHubRepo) SHA() string     { return r.sha }
func (r *GitHubRepo) Digest() string  { return "" }
func (r *GitHubRepo) Files() []string { return r.files }
func (r *GitHubRepo) Notes() []string { return r.notes }

func (r *GitHubRepo) Read(ctx context.Context, path string) ([]byte, bool, error) {
	if b, ok := r.cache[path]; ok {
		return b, b != nil, nil
	}
	sha, ok := r.blobs[path]
	if !ok {
		return nil, false, nil
	}
	b, err := r.client.Blob(ctx, r.repo, sha)
	if err != nil {
		return nil, false, err
	}
	if isBinary(b) {
		r.cache[path] = nil
		return nil, false, nil
	}
	r.cache[path] = b
	return b, true, nil
}

func (r *GitHubRepo) Changed(ctx context.Context, base string) (map[string]bool, error) {
	if base == "" || base == r.sha {
		return nil, nil
	}
	paths, truncated, cmpErr := r.client.Compare(ctx, r.repo, base, r.sha)
	if cmpErr != nil {
		// A base the repo no longer holds must not fail the run: the range only ranks.
		r.notes = append(r.notes, "Speccy could not compare this commit with "+base+", so the candidates carry no ranking: "+cmpErr.Error())
		return nil, nil //nolint:nilerr // the ranking is optional; the scan covers the whole tree
	}
	if truncated {
		r.notes = append(r.notes, "GitHub cut the comparison with "+base+", so the ranking covers part of the range.")
	}
	out := map[string]bool{}
	for _, p := range paths {
		out[p] = true
	}
	return out, nil
}

// FolderRepo reads a folder on disk. Speccy does not run git on the user's machine
// (DEC-018), so a folder run has no commit range and records a content digest.
type FolderRepo struct {
	root  string
	files []string
	notes []string
	sum   string
}

// NewFolderRepo lists the folder and applies the profile's scan bounds.
func NewFolderRepo(root string, limits profile.Verify) (*FolderRepo, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	s := &scan{limits: limits}
	h := sha256.New()
	r := &FolderRepo{root: abs}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			// A file Speccy cannot list is a file it did not read, and a target in it fails.
			return nil //nolint:nilerr // one unreadable entry must not end the scan
		}
		rel, relErr := filepath.Rel(abs, p)
		if relErr != nil {
			return nil //nolint:nilerr // a path outside the root is not part of the repo
		}
		rel = filepath.ToSlash(rel)
		fi, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a file that vanished during the walk is not in the scan
		}
		if !s.keep(rel, fi.Size(), false) {
			return nil
		}
		r.files = append(r.files, rel)
		fmt.Fprintf(h, "%s:%d\n", rel, fi.Size())
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(r.files)
	r.notes = append(s.notes, "This run read a folder, so it has no base commit and the candidates carry no ranking.")
	r.sum = hex.EncodeToString(h.Sum(nil))
	return r, nil
}

func (r *FolderRepo) Name() string    { return r.root }
func (r *FolderRepo) SHA() string     { return "" }
func (r *FolderRepo) Digest() string  { return r.sum }
func (r *FolderRepo) Files() []string { return r.files }
func (r *FolderRepo) Notes() []string { return r.notes }

func (r *FolderRepo) Read(_ context.Context, path string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(path)))
	if err != nil {
		// Speccy did not read the file, so every target in it fails with that fault.
		return nil, false, nil //nolint:nilerr // a missing file is a failed target, not a failed run
	}
	if isBinary(b) {
		return nil, false, nil
	}
	return b, true, nil
}

func (r *FolderRepo) Changed(context.Context, string) (map[string]bool, error) { return nil, nil }
