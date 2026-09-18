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

// Root is the folder that local mode serves.
type Root struct {
	dir string // absolute, symlinks resolved
}

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
	return &Root{dir: abs}, nil
}

// Dir returns the absolute root folder.
func (r *Root) Dir() string { return r.dir }

// Bundle is one bundle folder found by a scan.
type Bundle struct {
	// Dir is the folder relative to the root, with / separators. The root itself is ".".
	Dir   string
	Main  source.MainDoc
	Files []source.File
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
	// bundleDirs holds every folder with a main doc, valid or not. Their files never count as
	// assets of a parent bundle.
	bundleDirs map[string]bool
}

// skipDir reports whether the scan ignores a folder: hidden folders (.git, .speccy) and
// node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Scan finds every bundle under the root. A folder is a bundle when exactly one markdown file
// directly in it has a frontmatter type (REQ-001). A folder with two or more is a problem.
func (r *Root) Scan() (*Scan, error) {
	s := &Scan{bundleDirs: map[string]bool{}}
	var dirs []string
	err := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == r.dir {
				return err
			}
			s.Problems = append(s.Problems, Problem{Path: r.rel(p), Message: err.Error()})
			return nil
		}
		if d.IsDir() {
			if p != r.dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			dirs = append(dirs, r.rel(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, dir := range dirs {
		mains, problems := r.mainDocCandidates(dir)
		s.Problems = append(s.Problems, problems...)
		if len(mains) > 0 {
			s.bundleDirs[dir] = true
		}
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
		files, problems, err := r.load(dir, s.bundleDirs)
		if err != nil {
			s.Problems = append(s.Problems, Problem{Path: dir, Message: err.Error()})
			continue
		}
		s.Problems = append(s.Problems, problems...)
		main, err := source.FindMainDoc(files)
		if err != nil {
			continue // reported above
		}
		s.Bundles = append(s.Bundles, Bundle{Dir: dir, Main: main, Files: files})
	}
	sort.Slice(s.Bundles, func(i, j int) bool { return s.Bundles[i].Dir < s.Bundles[j].Dir })
	sort.Slice(s.Problems, func(i, j int) bool { return s.Problems[i].Path < s.Problems[j].Path })
	return s, nil
}

// mainDocCandidates returns the markdown files directly in dir that have a frontmatter type.
// A file whose frontmatter does not parse, but has a type line, is a problem.
func (r *Root) mainDocCandidates(dir string) ([]string, []Problem) {
	entries, err := os.ReadDir(r.abs(dir))
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
		content, err := readCapped(r.abs(rel))
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

// Load reads the files of the bundle in dir. Nested bundle folders are not part of it.
func (r *Root) Load(s *Scan, dir string) ([]source.File, error) {
	files, _, err := r.load(dir, s.bundleDirs)
	return files, err
}

func (r *Root) load(dir string, bundleDirs map[string]bool) ([]source.File, []Problem, error) {
	var files []source.File
	var problems []Problem
	base := r.abs(dir)
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := r.rel(p)
		if d.IsDir() {
			if p != base && (skipDir(d.Name()) || bundleDirs[rel]) {
				return filepath.SkipDir
			}
			return nil
		}
		// Symlinks and special files are not followed: they can point outside the root.
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		content, err := readCapped(p)
		if errors.Is(err, errTooLarge) {
			problems = append(problems, Problem{Path: rel, Message: fmt.Sprintf("The file is larger than %d MB. Speccy does not read it.", MaxFileBytes>>20)})
			return nil
		}
		if err != nil {
			return err
		}
		inBundle, _ := filepath.Rel(base, p)
		files = append(files, source.File{Path: filepath.ToSlash(inBundle), Content: content})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	source.Sort(files)
	return files, problems, nil
}

// IsBundleDir reports whether a scan found a main doc in dir.
func (s *Scan) IsBundleDir(dir string) bool { return s.bundleDirs[dir] }

var errTooLarge = errors.New("file too large")

func readCapped(p string) ([]byte, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileBytes {
		return nil, errTooLarge
	}
	return os.ReadFile(p)
}

// Persist applies op to the bundle in dir on disk. Files is the bundle before op, and the
// caller has checked op with source.Apply.
func (r *Root) Persist(s *Scan, dir string, op source.Op) error {
	target := func(p string) (string, error) {
		clean, err := source.CleanPath(p)
		if err != nil {
			return "", err
		}
		// A path must not reach into a nested bundle.
		parts := strings.Split(clean, "/")
		for i := 1; i < len(parts); i++ {
			if s.bundleDirs[path.Join(dir, strings.Join(parts[:i], "/"))] {
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

func (r *Root) rel(abs string) string {
	rel, err := filepath.Rel(r.dir, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

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
