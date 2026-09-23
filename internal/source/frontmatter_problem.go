package source

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// FrontmatterProblem says why Speccy cannot read a doc's frontmatter, and what to write
// instead. Both are empty when Speccy reads the block, or when the doc has none (#76).
func FrontmatterProblem(content []byte) (problem, fix string) {
	raw, _, form := section.Frontmatter(content)
	if raw == nil {
		if hiddenBlock(content) {
			return "This doc has a frontmatter block inside <!-- -->, in a form or a place that Speccy does not read, so Speccy does not see its type or its links.",
				"Put the block at the start of the file: a <!-- line, the --- line, the keys, the --- line, and a --> line."
		}
		return "", ""
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "The frontmatter does not parse: " + yamlReason(err) + ".", "Fix the frontmatter so that it is valid YAML or JSON."
	}
	if len(doc.Content) == 0 {
		return "", ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "The frontmatter is not a map of keys.", "Write the frontmatter as keys and values."
	}
	var probe struct {
		Type  string `yaml:"type"`
		Size  string `yaml:"size"`
		Title string `yaml:"title"`
	}
	if err := root.Decode(&probe); err != nil {
		return "The frontmatter does not parse: " + yamlReason(err) + ".", "Give type, size and title as text."
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "links" {
			continue
		}
		if why := linksProblem(root.Content[i+1]); why != "" {
			return "Speccy does not read the links in the frontmatter: " + why + ".", linkFormat(form.JSON)
		}
	}
	return "", ""
}

// linksProblem says why a links value is not a list of kind and target pairs, or "".
func linksProblem(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		return ""
	}
	switch n.Kind {
	case yaml.SequenceNode:
	case yaml.MappingNode:
		return "links is a map, not a list"
	default:
		return "links is text, not a list"
	}
	for i, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			return fmt.Sprintf("link %d is not a pair of kind and target", i+1)
		}
		var l Link
		var other []string
		for j := 0; j+1 < len(item.Content); j += 2 {
			switch k := item.Content[j].Value; k {
			case "kind", "target":
			default:
				other = append(other, k)
			}
		}
		if err := item.Decode(&l); err != nil {
			return fmt.Sprintf("link %d has a kind or a target that is not text", i+1)
		}
		switch {
		case strings.TrimSpace(l.Kind) == "" && len(other) > 0:
			return fmt.Sprintf("link %d has %s and no kind", i+1, strings.Join(other, ", "))
		case strings.TrimSpace(l.Kind) == "":
			return fmt.Sprintf("link %d has no kind", i+1)
		case !slices.Contains(LinkKinds, l.Kind):
			return fmt.Sprintf("link %d has the kind %q, and the kinds are %s", i+1, l.Kind, strings.Join(LinkKinds, ", "))
		case strings.TrimSpace(l.Target) == "":
			return fmt.Sprintf("link %d has no target", i+1)
		}
	}
	return ""
}

// linkFormat is the fix for a links value Speccy does not read, in the form of the block.
func linkFormat(json bool) string {
	if json {
		return `Write links as a list of kind and target pairs: "links": [{"kind": "implements", "target": "PRD - X.md"}]. The target is the path of the doc, relative to this doc.`
	}
	return "Write links as a list of kind and target pairs:\n\nlinks:\n  - kind: implements\n    target: \"PRD - X.md\"\n\nThe target is the path of the doc, relative to this doc."
}

// hiddenBlock reports whether an HTML comment in the doc holds a --- line: a frontmatter block
// that Speccy does not read. A comment inside a code block is text, so it does not count.
func hiddenBlock(content []byte) bool {
	root := section.Markdown().Parser().Parse(text.NewReader(content))
	found := false
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		b, ok := n.(*ast.HTMLBlock)
		if !entering || !ok || b.HTMLBlockType != ast.HTMLBlockType2 {
			return ast.WalkContinue, nil
		}
		lines := b.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			if strings.TrimSpace(string(seg.Value(content))) == "---" {
				found = true
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}

// yamlReason is a parse error without the library's prefix.
func yamlReason(err error) string {
	return strings.TrimPrefix(strings.TrimPrefix(err.Error(), "yaml: "), "unmarshal errors:\n  ")
}
