package source

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/alternayte/speccy/internal/engine/section"
)

// References returns the files a markdown doc points at, as paths relative to the folder that
// holds the doc. A link with a scheme, a host or a leading slash names no file of this repo. An
// anchor-only link names a place in the same doc.
func References(content []byte) []string {
	doc := section.Markdown().Parser().Parse(text.NewReader(content))
	seen := map[string]bool{}
	var out []string
	add := func(dest []byte) {
		p, ok := relativeTarget(string(dest))
		if !ok || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			add(v.Destination)
		case *ast.Image:
			add(v.Destination)
		}
		return ast.WalkContinue, nil
	})
	sort.Strings(out)
	return out
}

// relativeTarget cleans one destination into a path relative to the doc's folder. ok is false
// when the destination names no file of this repo.
func relativeTarget(dest string) (string, bool) {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "#") {
		return "", false
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		p = u.Path
	}
	return path.Clean(p), true
}
