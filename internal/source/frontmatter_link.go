package source

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// AddLink adds one link to the doc's frontmatter links, and gives a doc with no frontmatter a
// block. A link with the same kind and target is not added twice. The rest of the frontmatter
// keeps its keys; YAML formatting may change.
func AddLink(content []byte, kind, target string) ([]byte, error) {
	raw, bodyStart := section.SplitFrontmatter(content)
	var doc yaml.Node
	if raw != nil {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("the frontmatter does not parse: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the frontmatter is not a map of keys")
	}
	var links *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "links" && root.Content[i+1].Kind == yaml.SequenceNode {
			links = root.Content[i+1]
		}
	}
	if links == nil {
		links = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "links"}, links)
	}
	for _, l := range links.Content {
		var got Link
		if l.Decode(&got) == nil && got.Kind == kind && got.Target == target {
			return content, nil
		}
	}
	links.Content = append(links.Content, &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "kind"}, {Kind: yaml.ScalarNode, Value: kind},
		{Kind: yaml.ScalarNode, Value: "target"}, {Kind: yaml.ScalarNode, Value: target},
	}})
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	_ = enc.Close()
	out := append([]byte("---\n"), buf.Bytes()...)
	out = append(out, "---\n"...)
	if raw == nil {
		return append(out, content...), nil
	}
	return append(out, content[bodyStart:]...), nil
}
