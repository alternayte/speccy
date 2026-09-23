package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alternayte/speccy/internal/engine/section"
)

// SetKeys returns the main doc with each key in keys set in its frontmatter, in the order
// given (REQ-135). A doc with no frontmatter gets one. An existing key keeps its place and
// takes the new value. The rest of the frontmatter keeps its keys; YAML formatting may change.
func SetKeys(content []byte, keys [][2]string) ([]byte, error) {
	return editFrontmatter(content, func(root *yaml.Node) (bool, error) {
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
		return true, nil
	})
}

// editFrontmatter applies edit to the root map of the doc's frontmatter, and writes the block
// back in the form it had: plain or in an HTML comment, YAML or JSON (#76). A doc with no
// frontmatter gets a plain YAML block. When edit returns false, the doc stays as it is.
func editFrontmatter(content []byte, edit func(root *yaml.Node) (bool, error)) ([]byte, error) {
	raw, bodyStart, form := section.Frontmatter(content)
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
	if changed, err := edit(root); err != nil || !changed {
		return content, err
	}
	var block []byte
	if form.JSON {
		var buf bytes.Buffer
		writeJSON(&buf, root, form.Indent, "")
		block = append(buf.Bytes(), '\n')
	} else {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return nil, err
		}
		_ = enc.Close()
		block = buf.Bytes()
	}
	out := append(append(append([]byte{}, form.Open...), block...), form.Close...)
	return append(out, content[bodyStart:]...), nil
}

// writeJSON writes n as indented JSON, with the keys in the order the block has them.
func writeJSON(buf *bytes.Buffer, n *yaml.Node, indent, at string) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		open, close, step := "{", "}", 2
		if n.Kind == yaml.SequenceNode {
			open, close, step = "[", "]", 1
		}
		if len(n.Content) == 0 {
			buf.WriteString(open + close)
			return
		}
		buf.WriteString(open + "\n")
		inner := at + indent
		for i := 0; i < len(n.Content); i += step {
			buf.WriteString(inner)
			if step == 2 {
				buf.WriteString(jsonString(n.Content[i].Value) + ": ")
			}
			writeJSON(buf, n.Content[i+step-1], indent, inner)
			if i+step < len(n.Content) {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(at + close)
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			buf.WriteString("null")
			return
		case "!!int", "!!float", "!!bool":
			if json.Valid([]byte(n.Value)) {
				buf.WriteString(n.Value)
				return
			}
		}
		buf.WriteString(jsonString(n.Value))
	default:
		buf.WriteString("null")
	}
}

// jsonString quotes s as a JSON string, and leaves <, > and & as they are.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
