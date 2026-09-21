// Package source holds bundle files in memory, the operations that change them, and the
// REQ-001 rule that finds the main doc. The adapters in source/local and source/db load and
// persist the files.
package source

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// File is one file of a bundle. Path is relative to the bundle folder, with / separators.
type File struct {
	Path    string
	Content []byte
}

// Op is one change to a bundle's files.
type Op struct {
	Kind    OpKind
	Path    string
	To      string // rename target
	Content []byte // write content
}

// OpKind names an operation.
type OpKind int

const (
	OpWrite OpKind = iota + 1
	OpDelete
	OpRename
)

// ErrNotFound means the file is not in the bundle.
var ErrNotFound = errors.New("file not found in the bundle")

// ErrExists means the rename target already exists.
var ErrExists = errors.New("a file with that path already exists in the bundle")

// CleanPath checks that p is a relative path inside a bundle and returns its clean form.
// It refuses absolute paths, .. segments, empty names, and backslashes.
func CleanPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("the path is empty")
	}
	if strings.Contains(p, `\`) || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("the path %q has a backslash or a NUL byte; use / between folders", p)
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("the path %q is absolute; use a path relative to the bundle", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("the path %q leaves the bundle", p)
		}
	}
	c := path.Clean(p)
	if c == "." || c == "" {
		return "", fmt.Errorf("the path %q names no file", p)
	}
	// The sidecar is Speccy's own file, and it is the one hidden path a bundle may hold (DEC-009).
	if !IsSidecar(c) {
		for _, seg := range strings.Split(c, "/") {
			if strings.HasPrefix(seg, ".") {
				return "", fmt.Errorf("the path %q has a hidden segment %q; Speccy does not manage hidden files", p, seg)
			}
		}
	}
	return c, nil
}

// Apply returns the files after op. It does not change files.
func Apply(files []File, op Op) ([]File, error) {
	p, err := CleanPath(op.Path)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(files)+1)
	idx := -1
	for i, f := range files {
		if f.Path == p {
			idx = i
		}
		out = append(out, f)
	}
	switch op.Kind {
	case OpWrite:
		if dirConflict(files, p) {
			return nil, fmt.Errorf("%q conflicts with a folder or file of the same name", p)
		}
		if idx >= 0 {
			out[idx] = File{Path: p, Content: op.Content}
		} else {
			out = append(out, File{Path: p, Content: op.Content})
		}
	case OpDelete:
		if idx < 0 {
			return nil, fmt.Errorf("%q: %w", p, ErrNotFound)
		}
		out = append(out[:idx], out[idx+1:]...)
	case OpRename:
		to, err := CleanPath(op.To)
		if err != nil {
			return nil, err
		}
		if idx < 0 {
			return nil, fmt.Errorf("%q: %w", p, ErrNotFound)
		}
		if to == p {
			return out, nil
		}
		for _, f := range files {
			if f.Path == to {
				return nil, fmt.Errorf("%q: %w", to, ErrExists)
			}
		}
		rest := append(append([]File{}, out[:idx]...), out[idx+1:]...)
		if dirConflict(rest, to) {
			return nil, fmt.Errorf("%q conflicts with a folder or file of the same name", to)
		}
		out[idx] = File{Path: to, Content: out[idx].Content}
	default:
		return nil, fmt.Errorf("unknown operation %d", op.Kind)
	}
	Sort(out)
	return out, nil
}

// dirConflict reports whether p is a folder of another file, or another file is a folder of p.
func dirConflict(files []File, p string) bool {
	for _, f := range files {
		if f.Path == p {
			continue
		}
		if strings.HasPrefix(f.Path, p+"/") || strings.HasPrefix(p, f.Path+"/") {
			return true
		}
	}
	return false
}

// Sort sorts files by path.
func Sort(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// Frontmatter is the part of a main doc's frontmatter that Speccy reads (SDD §10.2).
type Frontmatter struct {
	Type string `yaml:"type"`
	// Size is the scale the doc covers: feature, app, or initiative (REQ-134). An empty size
	// is inferred from the doc.
	Size  string `yaml:"size"`
	Title string `yaml:"title"`
	Links []Link `yaml:"links"`
}

// Waiver is an approved waiver in the sidecar (DEC-009, §9.3). It covers one check in one
// section while the section's hash is the same (REQ-074). An empty section is the whole doc.
type Waiver struct {
	Check       string   `yaml:"check"`
	Section     []string `yaml:"section,flow"`
	Reason      string   `yaml:"reason"`
	SectionHash string   `yaml:"section_hash"`
	RequestedBy string   `yaml:"requested_by,omitempty"`
}

// TraceAck acknowledges in the sidecar that an upstream trace ID is intentionally not covered
// (SDD §9.4).
type TraceAck struct {
	ID             string `yaml:"id"`
	Status         string `yaml:"status"` // covered_by | out_of_scope
	Target         string `yaml:"target"`
	Reason         string `yaml:"reason"`
	AcknowledgedBy string `yaml:"acknowledged_by"`
}

// Link is a frontmatter link to another bundle (REQ-050).
type Link struct {
	Kind   string `yaml:"kind"`
	Target string `yaml:"target"`
}

// Standalone acknowledges in the sidecar that a doc has no upstream doc (REQ-057).
type Standalone struct {
	Reason         string `yaml:"reason"`
	AcknowledgedBy string `yaml:"acknowledged_by"`
}

// MainDoc is the result of the REQ-001 rule.
type MainDoc struct {
	Path        string
	Frontmatter Frontmatter
	// Title is the frontmatter title, else the first level-1 heading, else "".
	Title string
}

// MainDocError is a REQ-001 error: a folder with zero or two or more main docs, or a
// markdown file whose frontmatter does not parse.
type MainDocError struct {
	Message string
	Files   []string
}

func (e *MainDocError) Error() string {
	if len(e.Files) == 0 {
		return e.Message
	}
	return e.Message + ": " + strings.Join(e.Files, ", ")
}

// IsMarkdown reports whether p is a markdown file.
func IsMarkdown(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown"
}

// ReadFrontmatter returns the frontmatter of a markdown file. ok is false when the file has
// no frontmatter block.
func ReadFrontmatter(content []byte) (fm Frontmatter, ok bool, err error) {
	raw, _ := section.SplitFrontmatter(content)
	if raw == nil {
		return Frontmatter{}, false, nil
	}
	if err := yaml.Unmarshal(raw, &fm); err != nil {
		return Frontmatter{}, true, err
	}
	fm.Type = strings.TrimSpace(fm.Type)
	return fm, true, nil
}

// FindMainDoc applies REQ-001 form (a): exactly one markdown file directly in the bundle
// folder has a type field in its frontmatter. Files in subfolders are assets.
func FindMainDoc(files []File) (MainDoc, error) {
	var found []MainDoc
	var bad []string
	for _, f := range files {
		if strings.Contains(f.Path, "/") || !IsMarkdown(f.Path) {
			continue
		}
		fm, ok, err := ReadFrontmatter(f.Content)
		if err != nil {
			bad = append(bad, f.Path)
			continue
		}
		if !ok || fm.Type == "" {
			continue
		}
		found = append(found, MainDoc{Path: f.Path, Frontmatter: fm, Title: docTitle(fm, f.Content)})
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		paths := make([]string, len(found))
		for i, m := range found {
			paths[i] = m.Path
		}
		sort.Strings(paths)
		return MainDoc{}, &MainDocError{Message: "more than one main doc", Files: paths}
	case len(bad) > 0:
		sort.Strings(bad)
		return MainDoc{}, &MainDocError{Message: "frontmatter that does not parse as YAML", Files: bad}
	default:
		return MainDoc{}, &MainDocError{Message: "no main doc"}
	}
}

// SingleFileMainDoc is the main doc of a single-file bundle (REQ-001 form b, REQ-130). Its
// profile is the frontmatter type, or else the mapped profile (T-093).
func SingleFileMainDoc(path string, content []byte, mapped string) (MainDoc, error) {
	fm, _, err := ReadFrontmatter(content)
	if err != nil {
		return MainDoc{}, &MainDocError{Message: "frontmatter that does not parse as YAML", Files: []string{path}}
	}
	if fm.Type == "" {
		fm.Type = mapped
	}
	return MainDoc{Path: path, Frontmatter: fm, Title: docTitle(fm, content)}, nil
}

// AssetsDir is the assets folder of a single-file bundle: "prd-payments.md" has
// "prd-payments.assets/" (REQ-131).
func AssetsDir(file string) string {
	return strings.TrimSuffix(file, path.Ext(file)) + ".assets"
}

func docTitle(fm Frontmatter, content []byte) string {
	if t := strings.TrimSpace(fm.Title); t != "" {
		return t
	}
	for _, s := range section.Parse(content).Sections {
		if s.Level == 1 {
			return s.Title
		}
	}
	return ""
}

// NeverASpec are the markdown files a repository keeps for other reasons. Neither the scan nor
// speccy init offers them as bundles.
var NeverASpec = map[string]bool{"readme.md": true, "changelog.md": true, "license.md": true, "contributing.md": true,
	"code_of_conduct.md": true, "security.md": true, "agents.md": true, "claude.md": true}

// AddTypeLine returns content with type: key in its frontmatter. It writes the frontmatter when
// the doc has none, and it changes nothing else. Every way into Speccy writes the same line.
func AddTypeLine(content []byte, key string) []byte {
	if fm, _ := section.SplitFrontmatter(content); fm == nil {
		return append([]byte("---\ntype: "+key+"\n---\n\n"), content...)
	}
	open := bytes.IndexByte(content, '\n') + 1
	return append(append(append([]byte{}, content[:open]...), []byte("type: "+key+"\n")...), content[open:]...)
}

// Skipped reports whether a markdown file is one the scan passes over: it is a markdown file,
// it names no type, and it is not a file a repository keeps for another reason.
func Skipped(p string, content []byte) bool {
	if !IsMarkdown(p) || NeverASpec[strings.ToLower(path.Base(p))] {
		return false
	}
	fm, _, err := ReadFrontmatter(content)
	return err == nil && fm.Type == ""
}
