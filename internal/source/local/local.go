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
	// repo is the tree that carried files come from, when it is wider than fsys: the whole
	// repo of a GitHub source, whose scan tree holds the source's own paths only.
	repo fs.FS
}

// WithRepo returns the root with repo as the tree that a bundle carries files from.
func (r *Root) WithRepo(repo fs.FS) *Root {
	c := *r
	c.repo = repo
	return &c
}

// repoFS is the tree that a bundle carries files from.
func (r *Root) repoFS() fs.FS {
	if r.repo != nil {
		return r.repo
	}
	return r.fsys
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

// Bundle is one spec doc found by a scan, with the files of its version. A bundle is the
// folder that holds one or more spec docs: every spec doc in one folder has the same Folder.
type Bundle struct {
	// Slug names the spec doc: its folder relative to the root ("." for the root) when it is
	// the only spec doc there and names its own type, and otherwise its path without ".md".
	Slug string
	// Folder is the slug of the bundle that holds the spec doc: its folder relative to the root.
	Folder string
	// Dir is the bundle's folder relative to the root. File paths are relative to it.
	Dir string
	// File is the spec doc, relative to Dir.
	File  string
	Main  source.MainDoc
	Files []source.File
	// Refs are the links of the bundle's markdown outside its folder, and to other spec docs,
	// with what the scan did with each one.
	Refs []source.Ref
	// Unnamed is a bundle that only exists because the user pointed Speccy at the file: it
	// names no type, and no map entry selects it (REQ-135). It is reviewed, never saved.
	Unnamed bool
}

// Names reports whether the path p (relative to the root) names the bundle: a folder names
// the bundles in it and the bundle it is inside; a file names the bundle it belongs to.
func (b Bundle) Names(p string, dir bool) bool {
	inside := func(prefix string) bool { return prefix != "." && strings.HasPrefix(p, prefix+"/") }
	if dir {
		if p == "." || b.Slug == p || strings.HasPrefix(b.Slug, p+"/") {
			return true
		}
		return b.File == "" && inside(b.Dir)
	}
	if path.Join(b.Dir, b.Main.Path) == p {
		return true
	}
	if b.File == "" {
		return inside(b.Dir)
	}
	assets := path.Join(b.Dir, source.AssetsDir(b.File))
	return p == assets || inside(assets)
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
	// bundleDirs holds every bundle folder. Their files never count as assets of a parent
	// bundle. docs holds every spec doc: a spec doc is never an asset of another spec doc.
	bundleDirs map[string]bool
	docs       map[string]bool
	bySlug     map[string]int
}

// skipDir reports whether the scan ignores a folder: hidden folders (.git, .speccy) and
// node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Scan finds every bundle under the root. A folder is a bundle when it directly holds one or
// more spec docs: a markdown file with a frontmatter type (REQ-001 form a; cfg.Bundles can limit
// the folders), or one that a cfg.Map glob selects (form b, REQ-130). A subfolder with no spec
// doc holds assets of the bundle above it; a subfolder with its own spec doc is its own bundle.
// Each spec doc's version holds the doc and every asset of its bundle.
func (r *Root) Scan(cfg source.RepoConfig) (*Scan, error) {
	s := &Scan{bundleDirs: map[string]bool{}, docs: map[string]bool{}, bySlug: map[string]int{}}
	var dirs []string
	mapped := map[string][]string{} // folder -> mapped files directly in it
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
				mapped[path.Dir(rel)] = append(mapped[path.Dir(rel)], path.Base(rel))
			} else {
				markdown = append(markdown, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The spec docs of each folder are found before any folder loads, so no bundle takes a
	// spec doc or a nested bundle as an asset.
	docsIn := map[string][]string{}
	for _, dir := range dirs {
		typed, problems := r.mainDocCandidates(dir)
		s.Problems = append(s.Problems, problems...)
		if !cfg.BundleFolderAllowed(dir) {
			typed = nil
		}
		files := append(typed, mapped[dir]...)
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		files = uniq(files)
		docsIn[dir] = files
		s.bundleDirs[dir] = true
		for _, f := range files {
			s.docs[path.Join(dir, f)] = true
		}
	}
	for _, dir := range dirs {
		files := docsIn[dir]
		if len(files) == 0 {
			continue
		}
		assets, problems, err := r.load(dir, s.skipFor(dir, ""))
		if err != nil {
			s.Problems = append(s.Problems, Problem{Path: dir, Message: err.Error()})
			continue
		}
		s.Problems = append(s.Problems, problems...)
		for _, f := range files {
			rel := path.Join(dir, f)
			b, err := r.loadDoc(dir, f, assets, cfg, len(files) == 1)
			if err != nil {
				s.Problems = append(s.Problems, Problem{Path: rel, Message: err.Error()})
				continue
			}
			s.Bundles = append(s.Bundles, b)
		}
	}
	// Each bundle carries the files that its markdown references above its folder. The walk
	// runs after every bundle exists, so a link to another spec doc is known.
	ignore := source.NewIgnore(r.repoFS())
	for i := range s.Bundles {
		b := &s.Bundles[i]
		var problems []Problem
		b.Files, b.Refs, problems = s.carry(r, b.Dir, b.Files, ignore)
		s.Problems = append(s.Problems, problems...)
	}
	sort.Slice(s.Bundles, func(i, j int) bool { return s.Bundles[i].Slug < s.Bundles[j].Slug })
	for i, b := range s.Bundles {
		s.bySlug[b.Slug] = i
	}
	sort.Slice(s.Problems, func(i, j int) bool { return s.Problems[i].Path < s.Problems[j].Path })
	// What the scan passed over: a markdown file with no type that is not a spec doc. An asset
	// of a bundle stays on the list until a person accepts a type or marks it Not a spec, so an
	// Accept on one doc in a folder never takes the other docs off the list (#73). A carried
	// file belongs to the doc that references it.
	taken := map[string]bool{}
	for _, b := range s.Bundles {
		taken[path.Join(b.Dir, b.Main.Path)] = true
		for _, f := range b.Files {
			if f.Carried() {
				taken[rootPath(b.Dir, f.Path)] = true
			}
		}
	}
	for _, m := range markdown {
		if taken[m] {
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

func uniq(sorted []string) []string {
	out := sorted[:0]
	for i, v := range sorted {
		if i == 0 || v != sorted[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// Bundle returns the spec doc with slug from the scan.
func (s *Scan) Bundle(slug string) (Bundle, bool) {
	i, ok := s.bySlug[slug]
	if !ok {
		return Bundle{}, false
	}
	return s.Bundles[i], true
}

// Folders returns the bundle folders of the scan, sorted.
func (s *Scan) Folders() []string {
	out := make([]string, 0, len(s.bundleDirs))
	for d := range s.bundleDirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// skipFor returns the paths the assets of the bundle in dir must not include: nested bundle
// folders, and every spec doc except keep.
func (s *Scan) skipFor(dir, keep string) func(rel string) bool {
	return func(rel string) bool {
		return (rel != dir && s.bundleDirs[rel]) || (s.docs[rel] && rel != keep)
	}
}

// loadDoc makes the spec doc file in the bundle folder dir: the doc and the bundle's assets.
// alone is true when the doc is the only spec doc in dir. Such a doc takes the folder's slug,
// unless a mapping selects it.
func (r *Root) loadDoc(dir, file string, assets []source.File, cfg source.RepoConfig, alone bool) (Bundle, error) {
	rel := path.Join(dir, file)
	content, err := r.readCapped(rel)
	if err != nil {
		return Bundle{}, err
	}
	profile, mapped := cfg.MappedProfile(rel)
	main, err := source.SingleFileMainDoc(file, content, profile)
	if err != nil {
		return Bundle{}, err
	}
	files := append([]source.File{{Path: file, Content: content}}, assets...)
	source.Sort(files)
	slug := dir
	if !alone || mapped {
		slug = strings.TrimSuffix(rel, path.Ext(rel))
	}
	return Bundle{Slug: slug, Folder: dir, Dir: dir, File: file, Main: main, Files: files}, nil
}

// mainDocCandidates returns the markdown files directly in dir that have a frontmatter type.
// A file whose frontmatter does not parse, but has a type line, is a problem.
func (r *Root) mainDocCandidates(dir string) ([]string, []Problem) {
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

// Load reads the current files of the spec doc with slug.
func (r *Root) Load(s *Scan, slug string) ([]source.File, error) {
	b, ok := s.Bundle(slug)
	if !ok {
		return nil, fmt.Errorf("no bundle %q on disk", slug)
	}
	files, _, err := r.load(b.Dir, s.skipFor(b.Dir, path.Join(b.Dir, b.File)))
	if err != nil {
		return nil, err
	}
	files, _, _ = s.carry(r, b.Dir, files, source.NewIgnore(r.repoFS()))
	return files, nil
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
		if strings.HasPrefix(inBundle, source.RepoDir+"/") {
			problems = append(problems, Problem{Path: rel, Message: fmt.Sprintf("Speccy keeps the repo files that a doc references in %s, so it does not read this file. Rename the folder.", source.RepoDir)})
			return nil
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
		if clean == b.File && op.Kind != source.OpWrite {
			return "", fmt.Errorf("%s is the spec doc; rename or delete it on disk", clean)
		}
		// A path must not reach into a nested bundle or name another spec doc.
		parts := strings.Split(clean, "/")
		for i := 1; i <= len(parts); i++ {
			rel := path.Join(dir, strings.Join(parts[:i], "/"))
			if i < len(parts) && s.bundleDirs[rel] {
				return "", fmt.Errorf("%q is inside another bundle", clean)
			}
			if i == len(parts) && s.docs[rel] && clean != b.File {
				return "", fmt.Errorf("%q is another spec doc in this bundle; open that doc to change it", clean)
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

// rootPath returns the path from the source root of the file p of the bundle in dir: a
// carried file in source.RepoDir names it already.
func rootPath(dir, p string) string {
	if rest, ok := strings.CutPrefix(p, source.RepoDir+"/"); ok {
		return rest
	}
	return path.Join(dir, p)
}

// under reports whether p, a path from the source root, is in the folder dir or below it.
func under(dir, p string) bool {
	return dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// carry adds to the files of the bundle in dir the files that its markdown references above
// the bundle folder, at source.RepoPath, to closure through each carried markdown file. It
// returns the refs of the bundle's own markdown: each link above the folder, and each link to
// a spec doc of another bundle, with what the scan did (docs/specs/carry-repo-files.md).
//
// A link to a spec doc is never carried: two bundles must not hold one text. A link above the
// source root, to a hidden file, or to nothing stays a broken link. A file that git ignores is
// never carried. The bundle size limit bounds the walk.
func (s *Scan) carry(r *Root, dir string, files []source.File, ignore *source.Ignore) ([]source.File, []source.Ref, []Problem) {
	var problems []Problem
	held := map[string]bool{}
	var total int64
	for _, f := range files {
		held[f.Path] = true
		total += int64(len(f.Content))
	}
	refs := map[string]source.Ref{}
	var queue []string
	for _, f := range files {
		if source.IsMarkdown(f.Path) {
			queue = append(queue, f.Path)
		}
	}
	content := map[string][]byte{}
	for _, f := range files {
		content[f.Path] = f.Content
	}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		own := !strings.HasPrefix(from, source.RepoDir+"/")
		fromRoot := rootPath(dir, from)
		for _, ref := range source.References(content[from]) {
			target := path.Clean(path.Join(path.Dir(from), ref))
			rel := path.Clean(path.Join(path.Dir(fromRoot), ref))
			if rel == ".." || strings.HasPrefix(rel, "../") {
				continue // above the source root: a broken link
			}
			note := func(state, p string) {
				if own {
					refs[target] = source.Ref{Target: target, State: state, Path: p}
				}
			}
			if s.docs[rel] {
				if rel != fromRoot {
					note(source.RefDoc, "")
				}
				continue
			}
			if under(dir, rel) {
				continue // in the folder: an asset of the bundle, or a broken link
			}
			carried := source.RepoPath(rel)
			if held[carried] {
				note(source.RefCarried, carried)
				continue
			}
			if hidden(rel) {
				continue
			}
			if ignore.Ignored(rel) {
				note(source.RefIgnored, "")
				continue
			}
			info, err := fs.Stat(r.repoFS(), rel)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if info.Size() > MaxFileBytes || total+info.Size() > source.DefaultLimits.BundleBytes {
				problems = append(problems, Problem{Path: rel,
					Message: fmt.Sprintf("%s references this file, and the bundle limit stops Speccy from taking it.", rootPath(dir, from))})
				continue
			}
			body, err := fs.ReadFile(r.repoFS(), rel)
			if err != nil {
				continue
			}
			held[carried] = true
			total += int64(len(body))
			content[carried] = body
			files = append(files, source.File{Path: carried, Content: body, CarriedBy: from})
			note(source.RefCarried, carried)
			if source.IsMarkdown(carried) {
				queue = append(queue, carried)
			}
		}
	}
	source.Sort(files)
	out := make([]source.Ref, 0, len(refs))
	for _, r := range refs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return files, out, problems
}

// hidden reports whether a path has a hidden segment or a folder that the scan skips.
func hidden(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "node_modules" {
			return true
		}
	}
	return false
}
