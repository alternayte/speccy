package source

import (
	"bytes"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// AddWaiver returns the main doc with w in its frontmatter's waivers list (DEC-009). A waiver
// for the same check and section is replaced. A doc with no frontmatter gets one. The rest of
// the frontmatter keeps its keys and comments; YAML formatting may change.
func AddWaiver(content []byte, w Waiver) ([]byte, error) {
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
	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "waivers" {
			list = root.Content[i+1]
		}
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		if list == nil {
			list = &yaml.Node{Kind: yaml.SequenceNode}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "waivers"}, list)
		} else {
			*list = yaml.Node{Kind: yaml.SequenceNode}
		}
	}
	var item yaml.Node
	if err := item.Encode(w); err != nil {
		return nil, err
	}
	replaced := false
	for i, n := range list.Content {
		var old Waiver
		if n.Decode(&old) == nil && old.Check == w.Check && slices.Equal(old.Section, w.Section) {
			list.Content[i] = &item
			replaced = true
		}
	}
	if !replaced {
		list.Content = append(list.Content, &item)
	}
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
