package source

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// SetKeys returns the main doc with each key in keys set in its frontmatter, in the order
// given (REQ-135). A doc with no frontmatter gets one. An existing key keeps its place and
// takes the new value. The rest of the frontmatter keeps its keys; YAML formatting may change.
func SetKeys(content []byte, keys [][2]string) ([]byte, error) {
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
	// Set in reverse, because a new key goes to the front: the last one prepended ends up
	// first, so the caller's order is what the file shows.
	for i := len(keys) - 1; i >= 0; i-- {
		kv := keys[i]
		set := false
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == kv[0] {
				root.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: kv[1]}
				set = true
			}
		}
		if !set {
			pair := []*yaml.Node{{Kind: yaml.ScalarNode, Value: kv[0]}, {Kind: yaml.ScalarNode, Value: kv[1]}}
			// A new key goes after the type, which names the doc, else at the front.
			at := 0
			for i := 0; i+1 < len(root.Content); i += 2 {
				if root.Content[i].Value == "type" {
					at = i + 2
				}
			}
			root.Content = append(root.Content[:at:at], append(pair, root.Content[at:]...)...)
		}
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
