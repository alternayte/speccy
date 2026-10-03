package source

import (
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
)

// Ignore holds the .gitignore files of a tree, from its root down. Speccy never carries a file
// that git ignores into a bundle: ignore rules mark the files that must not leave the machine,
// and a CI checkout does not hold them (docs/specs/carry-repo-files.md). Speccy never runs git,
// so it reads the files itself.
type Ignore struct {
	fsys  fs.FS
	mu    sync.Mutex
	rules map[string][]ignoreRule // by the folder of the .gitignore file
}

type ignoreRule struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// NewIgnore returns the ignore rules of the tree fsys.
func NewIgnore(fsys fs.FS) *Ignore {
	return &Ignore{fsys: fsys, rules: map[string][]ignoreRule{}}
}

// Ignored reports whether git ignores the file at rel, a path relative to the root of the tree.
// A file in an ignored folder is ignored, as git does not look inside such a folder.
func (g *Ignore) Ignored(rel string) bool {
	segs := strings.Split(rel, "/")
	for i := 1; i <= len(segs); i++ {
		if g.match(strings.Join(segs[:i], "/"), i < len(segs)) {
			return true
		}
	}
	return false
}

// match applies the rules of each .gitignore from the root down to the folder of p. The last
// rule that matches decides, so a deeper file and a later line win.
func (g *Ignore) match(p string, dir bool) bool {
	ignored := false
	parent := path.Dir(p)
	folders := []string{"."}
	if parent != "." {
		segs := strings.Split(parent, "/")
		for i := 1; i <= len(segs); i++ {
			folders = append(folders, strings.Join(segs[:i], "/"))
		}
	}
	for _, f := range folders {
		rel := p
		if f != "." {
			rel = strings.TrimPrefix(p, f+"/")
		}
		for _, r := range g.rulesOf(f) {
			if r.matches(rel, dir) {
				ignored = !r.negate
			}
		}
	}
	return ignored
}

func (g *Ignore) rulesOf(folder string) []ignoreRule {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rules, ok := g.rules[folder]; ok {
		return rules
	}
	content, err := fs.ReadFile(g.fsys, path.Join(folder, ".gitignore"))
	var rules []ignoreRule
	if err == nil {
		rules = parseIgnore(string(content))
	}
	g.rules[folder] = rules
	return rules
}

// parseIgnore reads the lines of one .gitignore file.
func parseIgnore(content string) []ignoreRule {
	var out []ignoreRule
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasSuffix(line, `\ `) {
			line = strings.TrimRight(line, " ")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r ignoreRule
		switch {
		case strings.HasPrefix(line, "!"):
			r.negate, line = true, line[1:]
		case strings.HasPrefix(line, `\!`), strings.HasPrefix(line, `\#`):
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimSuffix(line, "/")
		}
		// A pattern with a slash before its end is relative to the folder of the .gitignore.
		if strings.Contains(line, "/") {
			r.anchored, line = true, strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		r.pattern = strings.ReplaceAll(line, `\ `, " ")
		out = append(out, r)
	}
	return out
}

// matches reports whether the rule matches p, a path relative to the folder of its file.
func (r ignoreRule) matches(p string, dir bool) bool {
	if r.dirOnly && !dir {
		return false
	}
	if r.anchored {
		ok, _ := doublestar.Match(r.pattern, p)
		return ok
	}
	ok, _ := doublestar.Match(r.pattern, path.Base(p))
	return ok
}
