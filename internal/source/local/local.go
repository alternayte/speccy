// Package local is the local source: bundles are folders on disk under one root folder.
// Files on disk are the truth (DEC-018). Speccy never runs git.
package local

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alternayte/speccy/internal/source"
)

// MaxFileBytes is the largest file the local source reads. Bigger files are skipped and
// listed as a problem, so one stray file cannot fill memory on each scan.
const MaxFileBytes = source.MaxFileBytes

// Root is the folder that local mode serves. Scans read it through fsys, so a tree that is
// not on disk (a GitHub repo) scans with the same rules; such a root has no dir and cannot
// persist changes.
type Root struct {
	dir  string // absolute, symlinks resolved; "" for a tree that is not on disk
	fsys fs.FS
}

// FromFS returns a read-only root over fsys, for scans only.
func FromFS(fsys fs.FS) *Root { return &Root{fsys: fsys} }

// Open returns the root at dir.
func Open(dir string) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", abs)
	}
	return &Root{dir: abs, fsys: os.DirFS(abs)}, nil
}

// Dir returns the absolute root folder.
func (r *Root) Dir() string { return r.dir }

// Bundle is one bundle found by a scan.
type Bundle struct {
	// Slug names the bundle: its folder relative to the root ("." for the root), or for a
	// single-file bundle the file path without ".md".
	Slug string
	// Dir is the bundle's folder relative to the root. File paths are relative to it.
	Dir string
	// File is the main doc of a single-file bundle, and "" for a folder bundle.
	File  string
	Main  source.MainDoc
	Files []source.File
	// Unnamed is a bundle that only exists because the user pointed Speccy at the file: it
	// names no type, and no map entry selects it (REQ-135). It is reviewed, never saved.
	Unnamed bool
}

// Allows reports whether a single-file bundle may hold path: its main doc, or a file in its
// assets folder. A folder bundle allows any path.
func (b Bundle) Allows(p string) bool {
	if b.File == "" {
		return true
	}
	return p == b.File || strings.HasPrefix(p, source.AssetsDir(b.File)+"/")
}

// Problem is a folder or file that the scan could not use.
type Problem struct {
	Path    string
	Message string
}

// Scan is the result of one scan of the root.
type Scan struct {
	Bundles  []Bundle
	Problems []Problem
	// Skipped are the markdown files the scan passed over because they name no type. The app
	// offers to adopt them (REQ-001).
	Skipped []string
	// bundleDirs holds every folder bundle, valid or not. Their files never count as assets of
	// a parent bundle. excluded holds mapped files and their assets folders, which belong to
	// single-file bundles.
	bundleDirs map[string]bool
	excluded   map[string]bool
	bySlug     map[string]int
}

// skipDir reports whether the scan ignores a folder: hidden folders (.git, .speccy) and
// node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Scan finds every bundle under the root. A folder is a bundle when exactly one markdown file
// directly in it has a frontmatter type (REQ-001 form a); cfg.Bundles can limit the folders.
// A markdown file that a cfg.Map glob selects is a single-file bundle (form b, REQ-130).
func (r *Root) Scan(cfg source.RepoConfig) (*Scan, error) {
	s := &Scan{bundleDirs: map[string]bool{}, excluded: map[string]bool{}, bySlug: map[string]int{}}
	var dirs []string
	var mapped []string
	var markdown []string
	err := fs.WalkDir(r.fsys, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			if rel == "." {
				return err
			}
			s.Problems = append(s.Problems, Problem{Path: rel, Message: err.Error()})
			return nil
		}
		if d.IsDir() {
			if rel != "." && skipDir(d.Name()) {
				return fs.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		}
		if d.Type().IsRegular() && source.IsMarkdown(rel) {
			if _, ok := cfg.MappedProfile(rel); ok {
				mapped = append(mapped, rel)
			} else {
				markdown = append(markdown, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, m := range mapped {
		s.excluded[m] = true
		s.excluded[source.AssetsDir(m)] = true
	}

	for _, dir := range dirs {
		if s.excluded[dir] {
			continue
		}
		mains, problems := r.mainDocCandidates(dir, s.excluded)
		s.Problems = append(s.Problems, problems...)
		if len(mains) == 0 || !cfg.BundleFolderAllowed(dir) {
			continue
		}
		s.bundleDirs[dir] = true
		if len(mains) > 1 {
			s.Problems = append(s.Problems, Problem{
				Path:    dir,
				Message: "This folder has more than one main doc: " + strings.Join(mains, ", ") + ". Remove the type field from all but one.",
			})
		}
	}
	for _, dir := range dirs {
		if !s.bundleDirs[dir] {
			continue
		}
		files, problems, err := r.load(dir, s.skipFor(dir))
		if err != nil {
			s.Problems = append(s.Problems, Problem{Path: dir, Message: err.Error()})
			continue
		}
		s.Problems = append(s.Problems, problems...)
		main, err := source.FindMainDoc(files)
		if err != nil {
			continue // reported above
		}
		s.Bundles = append(s.Bundles, Bundle{Slug: dir, Dir: dir, Main: main, Files: files})
	}
	for _, m := range mapped {
		b, problems, err := r.loadSingle(m, cfg)
		s.Problems = append(s.Problems, problems...)
		if err != nil {
			s.Problems = append(s.Problems, Problem{Path: m, Message: err.Error()})
			continue
		}
		s.Bundles = append(s.Bundles, b)
	}
	// Every bundle takes the files its markdown points at (the carried files). The walk runs
	// after both kinds of bundle exist, so a reference to another bundle's doc is known.
	for i := range s.Bundles {
		s.Problems = append(s.Problems, s.carry(r, &s.Bundles[i], source.DefaultLimits.BundleBytes)...)
	}
	sort.Slice(s.Bundles, func(i, j int) bool { return s.Bundles[i].Slug < s.Bundles[j].Slug })
	for i, b := range s.Bundles {
		s.bySlug[b.Slug] = i
	}
	sort.Slice(s.Problems, func(i, j int) bool { return s.Problems[i].Path < s.Problems[j].Path })
	// What the scan passed over: a markdown file with no type, in no bundle.
	inBundle := map[string]bool{}
	for _, b := range s.Bundles {
		for _, f := range b.Files {
			inBundle[path.Join(b.Dir, f.Path)] = true
		}
		inBundle[b.Slug] = true
	}
	for _, m := range markdown {
		if inBundle[m] {
			continue
		}
		content, err := fs.ReadFile(r.fsys, m)
		if err != nil || !source.Skipped(m, content) {
			continue
		}
		s.Skipped = append(s.Skipped, m)
	}
	sort.Strings(s.Skipped)
	return s, nil
}

// Bundle returns the bundle with slug from the scan.
func (s *Scan) Bundle(slug string) (Bundle, bool) {
	i, ok := s.bySlug[slug]
	if !ok {
		return Bundle{}, false
	}
	return s.Bundles[i], true
}

// skipFor returns the paths a folder bundle in dir must not include: nested bundle folders,
// mapped files, and their assets folders.
func (s *Scan) skipFor(dir string) func(rel string) bool {
	return func(rel string) bool {
		return (rel != dir && s.bundleDirs[rel]) || s.excluded[rel]
	}
}

// loadSingle reads a single-file bundle: the mapped file and its assets folder (REQ-131).
func (r *Root) loadSingle(rel string, cfg source.RepoConfig) (Bundle, []Problem, error) {
	dir := path.Dir(rel)
	file := path.Base(rel)
	content, err := r.readCapped(rel)
	if err != nil {
		return Bundle{}, nil, err
	}
	files := []source.File{{Path: file, Content: content}}
	var problems []Problem
	assets := path.Join(dir, source.AssetsDir(file))
	if info, err := fs.Stat(r.fsys, assets); err == nil && info.IsDir() {
		more, p, err := r.load(assets, func(string) bool { return false })
		if err != nil {
			return Bundle{}, nil, err
		}
		problems = p
		for _, f := range more {
			files = append(files, source.File{Path: source.AssetsDir(file) + "/" + f.Path, Content: f.Content})
		}
	}
	profile, _ := cfg.MappedProfile(rel)
	main, err := source.SingleFileMainDoc(file, content, profile)
	if err != nil {
		return Bundle{}, problems, err
	}
	source.Sort(files)
	slug := strings.TrimSuffix(rel, path.Ext(rel))
	return Bundle{Slug: slug, Dir: dir, File: file, Main: main, Files: files}, problems, nil
}

// mainDocCandidates returns the markdown files directly in dir that have a frontmatter type.
// A file whose frontmatter does not parse, but has a type line, is a problem.
func (r *Root) mainDocCandidates(dir string, excluded map[string]bool) ([]string, []Problem) {
	entries, err := fs.ReadDir(r.fsys, dir)
	if err != nil {
		return nil, []Problem{{Path: dir, Message: err.Error()}}
	}
	var mains []string
	var problems []Problem
	for _, e := range entries {
		if !e.Type().IsRegular() || !source.IsMarkdown(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rel := path.Join(dir, e.Name())
		if excluded[rel] {
			continue
		}
		content, err := r.readCapped(rel)
		if err != nil {
			continue // load reports it when the folder is a bundle
		}
		fm, ok, err := source.ReadFrontmatter(content)
		if err != nil {
			if bytes.Contains(content, []byte("\ntype:")) {
				problems = append(problems, Problem{Path: rel, Message: "The frontmatter does not parse as YAML: " + err.Error()})
			}
			continue
		}
		if ok && fm.Type != "" {
			mains = append(mains, e.Name())
		}
	}
	return mains, problems
}

// Load reads the current files of the bundle with slug.
func (r *Root) Load(s *Scan, slug string) ([]source.File, error) {
	b, ok := s.Bundle(slug)
	if !ok {
		return nil, fmt.Errorf("no bundle %q on disk", slug)
	}
	if b.File != "" {
		fresh, _, err := r.loadSingle(path.Join(b.Dir, b.File), source.RepoConfig{})
		return fresh.Files, err
	}
	files, _, err := r.load(b.Dir, s.skipFor(b.Dir))
	return files, err
}

// load reads the files under dir. skip names folders and files (relative to the root) to leave out.
func (r *Root) load(dir string, skip func(rel string) bool) ([]source.File, []Problem, error) {
	var files []source.File
	var problems []Problem
	err := fs.WalkDir(r.fsys, dir, func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != dir && (skipDir(d.Name()) || skip(rel)) {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks and special files are not followed: they can point outside the root.
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") || skip(rel) {
			return nil
		}
		content, err := r.readCapped(rel)
		if errors.Is(err, errTooLarge) {
			problems = append(problems, Problem{Path: rel, Message: fmt.Sprintf("The file is larger than %d MB. Speccy does not read it.", MaxFileBytes>>20)})
			return nil
		}
		if err != nil {
			return err
		}
		inBundle := rel
		if dir != "." {
			inBundle = strings.TrimPrefix(rel, dir+"/")
		}
		files = append(files, source.File{Path: inBundle, Content: content})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	source.Sort(files)
	return files, problems, nil
}

var errTooLarge = errors.New("file too large")

// errReadOnly is a write to a root that is not on disk.
var errReadOnly = errors.New("this tree is not on disk, so Speccy cannot write to it")

func (r *Root) readCapped(rel string) ([]byte, error) {
	info, err := fs.Stat(r.fsys, rel)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileBytes {
		return nil, errTooLarge
	}
	return fs.ReadFile(r.fsys, rel)
}

// Persist applies op to the bundle with slug on disk. The caller has checked op with
// source.Apply.
func (r *Root) Persist(s *Scan, slug string, op source.Op) error {
	if r.dir == "" {
		return errReadOnly
	}
	b, ok := s.Bundle(slug)
	if !ok {
		return fmt.Errorf("no bundle %q on disk", slug)
	}
	dir := b.Dir
	target := func(p string) (string, error) {
		clean, err := source.CleanPath(p)
		if err != nil {
			return "", err
		}
		if !b.Allows(clean) {
			return "", fmt.Errorf("%q is outside this single-file bundle; put assets in %s/", clean, source.AssetsDir(b.File))
		}
		if b.File != "" && clean == b.File && op.Kind != source.OpWrite {
			return "", fmt.Errorf("%s is the doc of this bundle; rename or delete it on disk", clean)
		}
		// A path must not reach into a nested bundle.
		parts := strings.Split(clean, "/")
		for i := 1; i <= len(parts); i++ {
			rel := path.Join(dir, strings.Join(parts[:i], "/"))
			if (i < len(parts) && s.bundleDirs[rel]) || (b.File == "" && s.excluded[rel]) {
				return "", fmt.Errorf("%q is inside another bundle", clean)
			}
		}
		return r.abs(path.Join(dir, clean)), nil
	}
	switch op.Kind {
	case source.OpWrite:
		dst, err := target(op.Path)
		if err != nil {
			return err
		}
		if err := r.mkdirInside(filepath.Dir(dst)); err != nil {
			return err
		}
		return writeAtomic(dst, op.Content)
	case source.OpDelete:
		dst, err := target(op.Path)
		if err != nil {
			return err
		}
		if err := r.checkInside(filepath.Dir(dst)); err != nil {
			return err
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
		r.removeEmptyParents(filepath.Dir(dst), r.abs(dir))
		return nil
	case source.OpRename:
		from, err := target(op.Path)
		if err != nil {
			return err
		}
		to, err := target(op.To)
		if err != nil {
			return err
		}
		if err := r.checkInside(filepath.Dir(from)); err != nil {
			return err
		}
		if err := r.mkdirInside(filepath.Dir(to)); err != nil {
			return err
		}
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("%q: %w", op.To, source.ErrExists)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		r.removeEmptyParents(filepath.Dir(from), r.abs(dir))
		return nil
	}
	return fmt.Errorf("unknown operation %d", op.Kind)
}

// CreateBundle writes files into the new folder name under the root. The folder must not exist.
func (r *Root) CreateBundle(name string, files []source.File) (string, error) {
	if r.dir == "" {
		return "", errReadOnly
	}
	dir, err := source.CleanPath(name)
	if err != nil {
		return "", err
	}
	if strings.Contains(dir, "/") {
		return "", fmt.Errorf("the bundle name %q has a /; use a single folder name", name)
	}
	abs := r.abs(dir)
	if err := os.Mkdir(abs, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("a folder named %q already exists; choose another name", dir)
		}
		return "", err
	}
	for _, f := range files {
		dst := filepath.Join(abs, filepath.FromSlash(f.Path))
		if err := r.mkdirInside(filepath.Dir(dst)); err != nil {
			return "", err
		}
		if err := writeAtomic(dst, f.Content); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (r *Root) abs(rel string) string { return filepath.Join(r.dir, filepath.FromSlash(rel)) }

// checkInside refuses a folder whose real path is outside the root, for example through a symlink.
func (r *Root) checkInside(dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if real != r.dir && !strings.HasPrefix(real, r.dir+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside the served folder", dir)
	}
	return nil
}

func (r *Root) mkdirInside(dir string) error {
	// Find the deepest folder that exists, check it, then create the rest.
	existing := dir
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	if err := r.checkInside(existing); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func (r *Root) removeEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func writeAtomic(dst string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".speccy-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(dst); err == nil {
		mode = info.Mode().Perm()
	}
	_ = os.Chmod(tmp.Name(), mode)
	return os.Rename(tmp.Name(), dst)
}

// ReadRepoFile reads a file by its path in the served folder. A missing file is empty content
// and no error, so a doc with no sidecar reads as no decisions (DEC-009).
func (r *Root) ReadRepoFile(rel string) ([]byte, error) {
	clean, err := repoFilePath(rel)
	if err != nil {
		return nil, err
	}
	content, err := fs.ReadFile(r.fsys, clean)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return content, err
}

// WriteRepoFile writes a file by its path in the served folder, making its folders. Empty
// content removes the file.
func (r *Root) WriteRepoFile(rel string, content []byte) error {
	if r.dir == "" {
		return errReadOnly
	}
	clean, err := repoFilePath(rel)
	if err != nil {
		return err
	}
	dst := r.abs(clean)
	if len(content) == 0 {
		if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		r.removeEmptyParents(filepath.Dir(dst), r.dir)
		return nil
	}
	if err := r.mkdirInside(filepath.Dir(dst)); err != nil {
		return err
	}
	return writeAtomic(dst, content)
}

// repoFilePath cleans a path that Speccy itself owns, such as a sidecar. It allows the hidden
// .speccy folder, which CleanPath refuses for the files of a bundle.
func repoFilePath(rel string) (string, error) {
	clean := path.Clean(rel)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q is outside the served folder", rel)
	}
	return clean, nil
}

// carry adds the files a bundle's markdown points at, to closure. A reference is a relative
// link or an image that resolves at or below the bundle's folder: a bundle path may hold no
// ".." segment, so a target above the folder has no path here, and the doc's link is never
// rewritten. A mapped doc is another bundle, and a file already in the bundle is not taken
// twice, so a cycle ends by itself.
func (s *Scan) carry(r *Root, b *Bundle, limit int64) []Problem {
	var problems []Problem
	held := map[string]bool{} // paths in the bundle, relative to it
	var total int64
	for _, f := range b.Files {
		held[f.Path] = true
		total += int64(len(f.Content))
	}
	// The queue holds the bundle-relative paths whose references are still to read.
	var queue []string
	for _, f := range b.Files {
		if source.IsMarkdown(f.Path) {
			queue = append(queue, f.Path)
		}
	}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		content := fileIn(b.Files, from)
		if content == nil {
			continue
		}
		for _, ref := range source.References(content) {
			// The reference is relative to the folder of the file that wrote it.
			inBundle := path.Join(path.Dir(from), ref)
			if strings.HasPrefix(inBundle, "..") || inBundle == "." || held[inBundle] {
				continue
			}
			rel := path.Join(b.Dir, inBundle)
			if s.excluded[rel] || s.bundleDirs[rel] {
				continue // another bundle holds it
			}
			info, err := fs.Stat(r.fsys, rel)
			if err != nil || !info.Mode().IsRegular() {
				continue // a link to nothing stays a broken link finding
			}
			if limit > 0 && total+info.Size() > limit {
				problems = append(problems, Problem{Path: rel,
					Message: fmt.Sprintf("%s references this file, and the bundle limit stops Speccy from taking it.", from)})
				continue
			}
			content, err := r.readCapped(rel)
			if err != nil {
				continue
			}
			held[inBundle] = true
			total += int64(len(content))
			b.Files = append(b.Files, source.File{Path: inBundle, Content: content, CarriedBy: from})
			if source.IsMarkdown(inBundle) {
				queue = append(queue, inBundle)
			}
		}
	}
	source.Sort(b.Files)
	return problems
}

// fileIn returns the content of one file of a bundle.
func fileIn(files []source.File, p string) []byte {
	for _, f := range files {
		if f.Path == p {
			return f.Content
		}
	}
	return nil
}
