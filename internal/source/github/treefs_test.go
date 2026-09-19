package github

import (
	"testing"
	"testing/fstest"
)

// The tree behaves as an fs.FS, and loads only what a caller reads.
func TestTreeFS(t *testing.T) {
	loads := 0
	entries := []Entry{
		{Path: "docs/prd/PRD.md", Type: "blob", Mode: "100644", SHA: "a", Size: 5},
		{Path: "docs/prd/assets/api.yaml", Type: "blob", Mode: "100644", SHA: "b", Size: 3},
		{Path: "docs/link", Type: "blob", Mode: "120000", SHA: "c", Size: 1},
		{Path: "src/main.go", Type: "blob", Mode: "100644", SHA: "d", Size: 4},
		{Path: "docs", Type: "tree", SHA: "e"},
	}
	content := map[string]string{"a": "# PRD", "b": "x: 1", "d": "main"}
	tf := NewTreeFS(entries, func(p string) bool { return Under(p, "docs") }, func(e Entry) ([]byte, error) {
		loads++
		return []byte(content[e.SHA]), nil
	})
	if err := fstest.TestFS(tf, "docs/prd/PRD.md", "docs/prd/assets/api.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := tf.Stat("src/main.go"); err == nil {
		t.Error("a path outside docs is in the tree")
	}
	if _, err := tf.Stat("docs/link"); err == nil {
		t.Error("a symlink is in the tree")
	}
	before := loads
	if _, err := tf.Stat("docs/prd/PRD.md"); err != nil || loads != before {
		t.Errorf("Stat loaded content: %v, %d loads", err, loads-before)
	}
}
