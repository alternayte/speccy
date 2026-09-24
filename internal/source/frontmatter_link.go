package source

import (
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// AddLink adds one link to the doc's frontmatter links, and gives a doc with no frontmatter a
// block. A link with the same kind and target is not added twice. The rest of the frontmatter
// keeps its keys and its form; YAML formatting may change.
func AddLink(content []byte, kind, target string) ([]byte, error) {
	return editFrontmatter(content, func(root *yaml.Node) (bool, error) {
		var links *yaml.Node
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "links" {
				continue
			}
			if v := root.Content[i+1]; v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null" {
				root.Content[i+1] = &yaml.Node{Kind: yaml.SequenceNode}
			}
			if root.Content[i+1].Kind != yaml.SequenceNode {
				return false, errors.New("the links value is not a list of kind and target pairs")
			}
			links = root.Content[i+1]
		}
		if links == nil {
			links = &yaml.Node{Kind: yaml.SequenceNode}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "links"}, links)
		}
		for _, l := range links.Content {
			var got Link
			if l.Decode(&got) == nil && got.Kind == kind && got.Target == target {
				return false, nil
			}
		}
		links.Content = append(links.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "kind"}, {Kind: yaml.ScalarNode, Value: kind},
			{Kind: yaml.ScalarNode, Value: "target"}, {Kind: yaml.ScalarNode, Value: target},
		}})
		return true, nil
	})
}

// RemoveLink takes every link with kind and target out of the doc's frontmatter links, and the
// links key with the last one. The rest of the frontmatter keeps its keys and its form; YAML
// formatting may change. ok is false when the frontmatter names no such link.
func RemoveLink(content []byte, kind, target string) (out []byte, ok bool, err error) {
	out, err = editFrontmatter(content, func(root *yaml.Node) (bool, error) {
		for i := 0; i+1 < len(root.Content); i += 2 {
			links := root.Content[i+1]
			if root.Content[i].Value != "links" || links.Kind != yaml.SequenceNode {
				continue
			}
			kept := links.Content[:0:0]
			for _, l := range links.Content {
				var got Link
				if l.Decode(&got) == nil && got.Kind == kind && strings.TrimSpace(got.Target) == target {
					ok = true
					continue
				}
				kept = append(kept, l)
			}
			if !ok {
				return false, nil
			}
			links.Content = kept
			if len(kept) == 0 {
				root.Content = append(root.Content[:i], root.Content[i+2:]...)
			}
			return true, nil
		}
		return false, nil
	})
	return out, ok, err
}
