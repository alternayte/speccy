package github

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// TreeFS is a repo tree as a read-only fs.FS. A file's content loads on the first read, so a
// scan fetches only the files it reads.
type TreeFS struct {
	files map[string]Entry
	dirs  map[string][]fs.DirEntry
	load  func(Entry) ([]byte, error)
}

// NewTreeFS indexes the blobs of a tree. keep chooses the paths to include; load fetches a
// blob's content.
func NewTreeFS(entries []Entry, keep func(path string) bool, load func(Entry) ([]byte, error)) *TreeFS {
	t := &TreeFS{files: map[string]Entry{}, dirs: map[string][]fs.DirEntry{".": nil}, load: load}
	seenDir := map[string]bool{".": true}
	var addDir func(d string)
	addDir = func(d string) {
		if seenDir[d] {
			return
		}
		seenDir[d] = true
		parent := path.Dir(d)
		addDir(parent)
		t.dirs[parent] = append(t.dirs[parent], dirEntry{name: path.Base(d), dir: true})
		t.dirs[d] = []fs.DirEntry{}
	}
	for _, e := range entries {
		// Blobs only: symlinks (mode 120000) and submodules are not followed.
		if e.Type != "blob" || e.Mode == "120000" || !keep(e.Path) {
			continue
		}
		dir := path.Dir(e.Path)
		addDir(dir)
		t.files[e.Path] = e
		t.dirs[dir] = append(t.dirs[dir], dirEntry{name: path.Base(e.Path), size: e.Size})
	}
	for d := range t.dirs {
		sort.Slice(t.dirs[d], func(i, j int) bool { return t.dirs[d][i].Name() < t.dirs[d][j].Name() })
	}
	return t
}

// Open implements fs.FS.
func (t *TreeFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if e, ok := t.files[name]; ok {
		return &treeFile{t: t, e: e}, nil
	}
	if entries, ok := t.dirs[name]; ok {
		return &treeDir{name: name, entries: entries}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadDir implements fs.ReadDirFS.
func (t *TreeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, ok := t.dirs[name]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return append([]fs.DirEntry(nil), entries...), nil
}

// Stat implements fs.StatFS, from the tree: it loads nothing.
func (t *TreeFS) Stat(name string) (fs.FileInfo, error) {
	if e, ok := t.files[name]; ok {
		return dirEntry{name: path.Base(name), size: e.Size}, nil
	}
	if _, ok := t.dirs[name]; ok {
		return dirEntry{name: path.Base(name), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

// ReadFile implements fs.ReadFileFS.
func (t *TreeFS) ReadFile(name string) ([]byte, error) {
	e, ok := t.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return t.load(e)
}

type dirEntry struct {
	name string
	dir  bool
	size int64
}

func (d dirEntry) Name() string               { return d.name }
func (d dirEntry) IsDir() bool                { return d.dir }
func (d dirEntry) Type() fs.FileMode          { return d.Mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d, nil }
func (d dirEntry) Size() int64                { return d.size }
func (d dirEntry) ModTime() time.Time         { return time.Time{} }
func (d dirEntry) Sys() any                   { return nil }
func (d dirEntry) Mode() fs.FileMode {
	if d.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type treeFile struct {
	t *TreeFS
	e Entry
	r *bytes.Reader
}

func (f *treeFile) Stat() (fs.FileInfo, error) {
	return dirEntry{name: path.Base(f.e.Path), size: f.e.Size}, nil
}

func (f *treeFile) Read(p []byte) (int, error) {
	if f.r == nil {
		b, err := f.t.load(f.e)
		if err != nil {
			return 0, err
		}
		f.r = bytes.NewReader(b)
	}
	return f.r.Read(p)
}

func (f *treeFile) Close() error { return nil }

type treeDir struct {
	name    string
	entries []fs.DirEntry
	pos     int
}

func (d *treeDir) Stat() (fs.FileInfo, error) {
	return dirEntry{name: path.Base(d.name), dir: true}, nil
}
func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}
func (d *treeDir) Close() error { return nil }

func (d *treeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.pos:]
	if n <= 0 {
		d.pos = len(d.entries)
		return append([]fs.DirEntry(nil), rest...), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(rest))
	d.pos += n
	return append([]fs.DirEntry(nil), rest[:n]...), nil
}

// Under reports whether p is dir or inside it. The empty dir or "." is the whole repo.
func Under(p, dir string) bool {
	dir = strings.Trim(dir, "/")
	return dir == "" || dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}
