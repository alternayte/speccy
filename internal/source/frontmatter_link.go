package source

import (
	"errors"

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
